package cli

import (
	"fmt"
	"net"
	"strconv"
)

func checkForwardedPorts(ports []int) error {
	for _, port := range ports {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("forwarded host address %s is unavailable: %w; stop the service using that port, or change ports.forward and rebuild the sandbox", address, err)
		}
		if err := listener.Close(); err != nil {
			return err
		}
	}
	return nil
}
