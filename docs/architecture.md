# ForgeHTTP architecture

Phase 1 accepts one TCP connection at a time and shuts down on context cancellation. Phase 2 parses one HTTP/1 request from that connection.

## Components

| Component | Responsibility | Now |
| --- | --- | --- |
| TCP listener | Bind a TCP socket and accept connections | `transport.Listen`, `server.ListenAndServe` |
| Connection manager | Deadlines, the active connection, shutdown | One connection at a time, deadlines, context shutdown |
| HTTP parser | Request line, headers, body, framing | `http1.ReadRequest` |
| Request handler | Validate a parsed request beyond framing | Later |
| Router | Method and path to application logic | Later |
| Response writer | Status, headers, body, `Content-Length` | Later. The server writes a plain-text dump of the parse so a client can finish |

`transport` opens sockets. `protocol/http1` reads requests. `server` owns the accept loop, deadlines, and shutdown. `net/http` is not part of this server.

## Process

```
main
  context cancelled on interrupt
        |
        v
ListenAndServe
  net.Listen("tcp", 127.0.0.1:8080)
        |
        v
Accept ---- one connection ---- ReadRequest ---- parse dump ---- Close
  ^                                                                |
  +----------------------------------------------------------------+
```

A client that has not finished its request is served before the next accepted client. Concurrent serving is a later phase. The accept loop is the place that change lands.

## Parser rules

- The request line is `METHOD SP target SP HTTP/1.0` or `HTTP/1.1`, ended by CRLF. The line is at most 8 KiB, including CRLF.
- The target is an origin-form path (`/hello?x=1`) or `*`.
- Header names are tokens and are stored in canonical form (`content-length` becomes `Content-Length`). Repeated fields stay as a list.
- The header block ends at a blank line and is at most 32 KiB.
- HTTP/1.1 requires exactly one Host field.
- A body is read only from a single `Content-Length`. The maximum body is 1 MB. A larger length is rejected before the body is read.
- Any `Transfer-Encoding` is rejected. If `Transfer-Encoding` and `Content-Length` are both present, the request is rejected as ambiguous framing. The body bytes are left unread.
- More than one `Content-Length` is ambiguous framing.

## Still fixed for later phases

- Concurrent connections: 100, when the server accepts concurrently.
- Idle timeout: 30 seconds. The server uses 30 seconds as the read deadline on the one active connection.
- Chunked transfer encoding stays unsupported until a chunked decoder exists.
- The sample body `{"message":"Hello from ForgeHTTP"}` is 34 bytes. A later response that sends it must use `Content-Length: 34`.

## Shutdown

Cancelling the context closes the listener and the active connection. `Accept` and a blocked `Read` return. `ListenAndServe` returns nil. Closing an already-closed listener or connection is ignored.

A temporary `Accept` error is logged and the loop continues. Any other `Accept` error stops the server.
