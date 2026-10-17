package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// TrustedProxyNetworks validates operator-supplied networks, never inferred private ranges.
func (c Config) TrustedProxyNetworks() ([]netip.Prefix, error) {
	if len(c.TrustedProxies) > 128 {
		return nil, fmt.Errorf("config: too many trusted proxies")
	}
	out := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, raw := range c.TrustedProxies {
		raw = strings.TrimSpace(raw)
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			ip, err := netip.ParseAddr(raw)
			if err != nil || ip.Zone() != "" {
				return nil, fmt.Errorf("config: invalid trusted proxy address")
			}
			ip = ip.Unmap()
			p = netip.PrefixFrom(ip, ip.BitLen())
		}
		if !p.IsValid() || p.Bits() == 0 || p.Addr().Is4In6() {
			return nil, fmt.Errorf("config: invalid trusted proxy network")
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
