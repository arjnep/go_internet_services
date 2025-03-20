package main

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var rootServers = map[string]string{
	"a.root-servers.net": "198.41.0.4",
	"b.root-servers.net": "192.228.79.201",
	"c.root-servers.net": "192.33.4.12",
	"d.root-servers.net": "128.8.10.90",
	"e.root-servers.net": "192.203.230.10",
	"f.root-servers.net": "192.5.5.241",
	"g.root-servers.net": "192.112.36.4",
	"h.root-servers.net": "128.63.2.53",
	"i.root-servers.net": "192.36.148.17",
	"j.root-servers.net": "192.58.128.30",
	"k.root-servers.net": "193.0.14.129",
	"l.root-servers.net": "199.7.83.42",
	"m.root-servers.net": "202.12.27.33",
}

func main() {
	domain := "example.com." // trailing . for lookup

	fmt.Println("Loading root server list:")
	for name, ip := range rootServers {
		fmt.Printf("-> %s (%s)\n", name, ip)
	}

	// random root server
	rootNames := make([]string, 0, len(rootServers))
	for name, _ := range rootServers {
		rootNames = append(rootNames, name)
	}
	rootName := rootNames[rand.Intn(len(rootNames))]

	fmt.Printf("\nStarting recursive lookup for %s\n", domain)
	recursiveLookup(domain, rootName, rootServers[rootName])
}

func recursiveLookup(domain, firstServerName string, firstServerIP string) {
	triedServers := map[string]bool{}
	serverName, serverIP := firstServerName, firstServerIP

	for {
		triedServers[serverIP] = true

		fmt.Printf("\nSending request to %s (%s)\n", serverName, serverIP)

		res, err := queryDNS(domain, serverIP)
		if err != nil {
			fmt.Println("Error:", err)

			newServerName, newServerIP := pickNewRootServer(triedServers)
			if newServerIP == "" {
				fmt.Println("No more root servers available. Stopping.")
				return
			}

			fmt.Printf("Retrying with a new root server: %s (%s)\n", newServerName, newServerIP)
			serverName, serverIP = newServerName, newServerIP
			continue
		}

		// response is authoritative ?
		if res.Authoritative {
			fmt.Println("\nReceived authoritative (AA) response:")
			for _, answer := range res.Answers {
				if answer.Header.Type == dnsmessage.TypeA {
					ip := net.IP(answer.Body.(*dnsmessage.AResource).A[:])
					fmt.Printf("-> Answer: A-record for %s = %v\n", domain, ip)
				} else if answer.Header.Type == dnsmessage.TypeAAAA {
					ip := net.IP(answer.Body.(*dnsmessage.AAAAResource).AAAA[:])
					fmt.Printf("-> Answer: AAAA-record for %s = %v\n", domain, ip)
				}
			}
			return
		}

		// next nameservers
		nextServers := getNextServers(res)
		if len(nextServers) == 0 {
			fmt.Println("No more name servers found, stopping.")
			return
		}

		// resolve ns names to ips
		serverName, serverIP = resolveNS(nextServers)
		if serverIP == "" {
			fmt.Println("Failed to resolve next NS IP, stopping.")
			return
		}
	}
}

func pickNewRootServer(tried map[string]bool) (string, string) {
	for name, ip := range rootServers {
		if !tried[ip] {
			return name, ip
		}
	}
	return "", ""
}

func queryDNS(domain, server string) (dnsmessage.Message, error) {

	dialer := net.Dialer{Timeout: 3 * time.Second}

	conn, err := dialer.Dial("udp", server+":53")
	if err != nil {
		return dnsmessage.Message{}, fmt.Errorf("timeout or connection error: %w", err)
	}
	defer conn.Close()

	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 1, RecursionDesired: false},
		Questions: []dnsmessage.Question{
			{Name: dnsmessage.MustNewName(domain), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
		},
	}

	query, err := msg.Pack()
	if err != nil {
		return dnsmessage.Message{}, err
	}

	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write(query)
	if err != nil {
		return dnsmessage.Message{}, fmt.Errorf("timeout or write error: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	response := make([]byte, 512)
	n, err := conn.Read(response)
	if err != nil {
		return dnsmessage.Message{}, fmt.Errorf("timeout or read error: %w", err)
	}

	var res dnsmessage.Message
	err = res.Unpack(response[:n])
	if err != nil {
		return dnsmessage.Message{}, err
	}

	return res, nil
}

func getNextServers(res dnsmessage.Message) []string {
	servers := []string{}
	var referralDomain string
	for _, ns := range res.Authorities {
		if ns.Header.Type == dnsmessage.TypeNS {
			nsName := ns.Body.(*dnsmessage.NSResource).NS.String()
			servers = append(servers, nsName)

			referralDomain = ns.Header.Name.String()
		}
	}

	if referralDomain == "" {
		referralDomain = "(unknown domain)"
	}

	// check if additional resolved ips
	resolvedIPs := map[string]string{}
	for _, extra := range res.Additionals {
		if extra.Header.Type == dnsmessage.TypeA {
			resolvedIPs[extra.Header.Name.String()] = net.IP(extra.Body.(*dnsmessage.AResource).A[:]).String()
		}
	}

	fmt.Println("\nReceived referral response - DNS servers for domain:", referralDomain)
	for _, ns := range servers {
		if ip, exists := resolvedIPs[ns]; exists {
			fmt.Printf("-> %s (%s)\n", ns, ip)
		} else {
			fmt.Printf("-> %s (no IP address)\n", ns)
		}
	}

	return servers
}

func resolveNS(servers []string) (string, string) {
	for _, ns := range servers {
		ip, err := net.LookupHost(strings.TrimSuffix(ns, ".")) // trailing dot
		if err == nil && len(ip) > 0 {
			fmt.Printf("\nResolved DNS server name %s to IP %s\n", ns, ip[0])
			return ns, ip[0]
		}
	}
	return "", ""
}
