package main

import (
	"fmt"
	"net"
	"net/url"
)

// A UDP connect chooses the route without transmitting telemetry or a datagram.
func interfaceForTarget(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	remote, err := net.ResolveUDPAddr("udp", u.Host)
	if err != nil {
		return "", err
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		return "", err
	}
	local := conn.LocalAddr().(*net.UDPAddr).IP
	conn.Close()
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if ip, _, err := net.ParseCIDR(address.String()); err == nil && ip.Equal(local) {
				return iface.Name, nil
			}
		}
	}
	return "", fmt.Errorf("no interface owns the route source address %s", local)
}
