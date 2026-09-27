package main

import "lfr-tunnel/pkg/client"

// healthCheckPortsFor picks the ports the health check should dial on the TARGET host.
//
// A named function with a test rather than a loop inline in main, because the defect it fixes
// was not in any loop: both slices are populated correctly and the wrong one was passed
// (#2270). What has to be asserted is the CHOICE -- that these ports are the ones a connection
// to the target would use, and not the interceptor's rewritten local ports -- and an inline loop
// gives a test nothing to hold.
func healthCheckPortsFor(targetMappings []client.PortMapping) []int {
	ports := make([]int, 0, len(targetMappings))
	for _, pm := range targetMappings {
		ports = append(ports, pm.LocalPort)
	}
	return ports
}
