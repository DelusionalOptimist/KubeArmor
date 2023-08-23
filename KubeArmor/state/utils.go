package state

import (
	"fmt"
	"net/url"
	"strings"
)

func ParseURL(address string) (string, string, error) {
	var host string
	port := "80"

	addr, err := url.Parse(address)
	if err != nil || addr.Host == "" {
		// URL without scheme
		u, repErr := url.ParseRequestURI("http://" + address)
		if repErr != nil {
			return "", "", fmt.Errorf("Error while parsing URL: %s", err)
		}

		addr = u
	}

	if addr.Port() != "" {
		host = strings.Split(addr.Host, ":")[0]
		port = addr.Port()
	}

	return host, port, nil
}
