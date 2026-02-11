package gateway

import (
	"context"
	"strconv"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
)

var (
	log              = clog.NewWithPlugin(thisPlugin)
	DefaultResources = []string{"Ingress", "Service"}
)

const thisPlugin = "k8s_gateway"

func init() {
	plugin.Register(thisPlugin, setup)
}

func setup(c *caddy.Controller) error {
	gw, err := parse(c)
	if err != nil {
		return plugin.Error(thisPlugin, err)
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
		case "soa":
			// Parse SOA timing values: soa <refresh> <retry> <expire>
			args := c.RemainingArgs()
			if len(args) != 3 {
				return nil, c.Errf("soa requires exactly 3 arguments: refresh retry expire")
			}
			
			refresh, err := strconv.Atoi(args[0])
			if err != nil {
				return nil, c.Errf("invalid refresh value: %v", err)
			}
			if refresh < 0 {
				return nil, c.Errf("refresh must be non-negative: %d", refresh)
			}
			
			retry, err := strconv.Atoi(args[1])
			if err != nil {
				return nil, c.Errf("invalid retry value: %v", err)
			}
			if retry < 0 {
				return nil, c.Errf("retry must be non-negative: %d", retry)
			}
			
			expire, err := strconv.Atoi(args[2])
			if err != nil {
				return nil, c.Errf("invalid expire value: %v", err)
			}
			if expire < 0 {
				return nil, c.Errf("expire must be non-negative: %d", expire)
			}
			
			gw.soaRefresh = uint32(refresh)
			gw.soaRetry = uint32(retry)
			gw.soaExpire = uint32(expire)


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
