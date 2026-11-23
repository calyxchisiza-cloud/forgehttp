// Package transport is the TCP boundary for ForgeHTTP.
// It opens listening sockets. It does not read or write HTTP.
package transport

import "net"

// Listen opens a TCP listener. addr is a host:port string such as 127.0.0.1:8080.
func Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
