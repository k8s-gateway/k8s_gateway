package gateway

import (
	"context"
	"strconv"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	sentry "github.com/getsentry/sentry-go"
)

var (
	log              = clog.NewWithPlugin(thisPlugin)
	DefaultResources = []string{"Ingress", "Service"}
)

const thisPlugin = "k8s_gateway"

// defaultSentryDSN is the project-wide DSN used for automatic error reporting.
// It is intentionally public — Sentry DSNs are write-only keys that can only
// submit events; they cannot read data or authenticate as anything else.
// Operators may override or disable this via `sentry <dsn>` / `sentry off`
// in their Corefile.
const defaultSentryDSN = "https://b349ce53c515d69659e2afedadb42bfc@o4511047748878336.ingest.de.sentry.io/4511047751827536"

func init() {
	plugin.Register(thisPlugin, setup)
}

func setup(c *caddy.Controller) error {
	gw, err := parse(c)
	if err != nil {
		return plugin.Error(thisPlugin, err)
	}

	if gw.sentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              gw.sentryDSN,
			SendDefaultPII:   false,
			AttachStacktrace: true,
			// BeforeSend scrubs all fields that could carry PII (user identity,
			// HTTP request headers/body, custom extras, breadcrumbs) before any
			// event is transmitted to Sentry.  This is a belt-and-suspenders
			// guard on top of the CaptureException calls used throughout the
			// plugin, which already limit transmitted context to error types and
			// static call-site tags (never DNS names, IP addresses, or raw
			// error strings).
			BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
				event.User = sentry.User{}
				event.Request = nil
				event.Extra = nil
				event.Breadcrumbs = nil
				return event
			},
		}); err != nil {
			return plugin.Error(thisPlugin, err)
		}
		log.Infof("Sentry error reporting initialized")
	}

	err = gw.RunKubeController(context.Background())
	if err != nil {
		return plugin.Error(thisPlugin, err)
	}
	gw.ExternalAddrFunc = gw.SelfAddress

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		gw.Next = next
		return gw
	})

	return nil
}

func parse(c *caddy.Controller) (*Gateway, error) {
	gw := newGateway()

	for c.Next() {
		zones := c.RemainingArgs()
		gw.Zones = zones

		if len(gw.Zones) == 0 {
			gw.Zones = make([]string, len(c.ServerBlockKeys))
			copy(gw.Zones, c.ServerBlockKeys)
		}

		for i, str := range gw.Zones {
			if host := plugin.Host(str).NormalizeExact(); len(host) != 0 {
				gw.Zones[i] = host[0]
			}
		}

		for c.NextBlock() {
			switch c.Val() {
			case "fallthrough":
				gw.Fall.SetZonesFromArgs(c.RemainingArgs())
			case "secondary":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.ArgErr()
				}
				gw.secondNS = args[0]
			case "resources":
				args := c.RemainingArgs()
				gw.updateResources(args)
				gw.SetConfiguredResources(args)

				if len(args) == 0 {
					return nil, c.Errf("Incorrectly formatted 'resource' parameter")
				}
			case "ttl":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.ArgErr()
				}
				t, err := strconv.Atoi(args[0])
				if err != nil {
					return nil, err
				}
				if t < 0 || t > 3600 {
					return nil, c.Errf("ttl must be in range [0, 3600]: %d", t)
				}
				gw.ttlLow = uint32(t)
			case "apex":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.ArgErr()
				}
				gw.apex = args[0]
			case "kubeconfig":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.ArgErr()
				}
				gw.configFile = args[0]
				if len(args) == 2 {
					gw.configContext = args[1]
				}

			case "ingressClasses":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.Errf("Incorrectly formatted 'ingressClasses' parameter")
				}
				gw.resourceFilters.ingressClasses = args

			case "gatewayClasses":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.Errf("Incorrectly formatted 'gatewayClasses' parameter")
				}
				gw.resourceFilters.gatewayClasses = args

			case "nodeAddressType":
				args := c.RemainingArgs()
				if len(args) != 1 {
					return nil, c.ArgErr()
				}
				if args[0] != "InternalIP" && args[0] != "ExternalIP" {
					return nil, c.Errf("nodeAddressType must be 'InternalIP' or 'ExternalIP', got: %s", args[0])
				}
				gw.nodeAddressType = args[0]

			case "sentry":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return nil, c.ArgErr()
				}
				// "sentry off" lets operators explicitly disable error reporting.
				if args[0] == "off" {
					gw.sentryDSN = ""
				} else {
					gw.sentryDSN = args[0]
				}

			default:
				return nil, c.Errf("Unknown property '%s'", c.Val())
			}
		}
	}

	if len(gw.ConfiguredResources) == 0 {
		log.Warningf("No resources specified in config. Using defaults: %s", DefaultResources)
		gw.updateResources(DefaultResources)
		gw.SetConfiguredResources(DefaultResources)
	}
	return gw, nil
}
