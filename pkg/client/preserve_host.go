package client

import "sort"

// SetPreserveHostPorts turns preserve_host on for these local ports only (#2560). It is how the
// client applies the automatic setting to a detected Liferay port without changing the Host
// every other published port receives -- a workspace also publishes client-extension dev
// servers, and those can refuse a Host they do not recognise.
//
// Independent of PreserveHost, which is the explicit, every-port form and wins over this.
func (e *InterceptorEngine) SetPreserveHostPorts(ports []int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.preserveHostPorts = portSet(ports)
}

// SetLiferayPorts records the local ports identified as Liferay (#2560, #2561).
func (e *InterceptorEngine) SetLiferayPorts(ports []int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.liferayPorts = portSet(ports)
}

// PreservesHostFor reports whether a request proxied to targetPort keeps the visitor's Host.
func (e *InterceptorEngine) PreservesHostFor(targetPort int) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.PreserveHost || e.preserveHostPorts[targetPort]
}

// IsLiferayPort reports whether targetPort was identified as Liferay.
func (e *InterceptorEngine) IsLiferayPort(targetPort int) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.liferayPorts[targetPort]
}

// PreserveHostPorts returns the ports preserve_host is on for automatically, sorted. It does not
// include the every-port setting; read PreserveHost for that.
func (e *InterceptorEngine) PreserveHostPorts() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.preserveHostPortsLocked()
}

// preserveHostPortsLocked is PreserveHostPorts for a caller already holding e.mu.
func (e *InterceptorEngine) preserveHostPortsLocked() []int {
	out := make([]int, 0, len(e.preserveHostPorts))
	for p := range e.preserveHostPorts {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func portSet(ports []int) map[int]bool {
	if len(ports) == 0 {
		return nil
	}
	set := make(map[int]bool, len(ports))
	for _, p := range ports {
		if p > 0 {
			set[p] = true
		}
	}
	return set
}
