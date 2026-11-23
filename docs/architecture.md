# ForgeHTTP architecture

Milestone 01 implements the TCP listener and a one-line connection exchange. The other components are named here so later milestones have a place to land. They are not in this tree yet.

## Components

| Component | Responsibility | Milestone 01 |
| --- | --- | --- |
| TCP listener | Bind a TCP socket and accept connections | `transport.Listen`, `server.ListenAndServe` |
| Connection manager | Deadlines, the active connection, shutdown | One connection at a time, deadlines, context shutdown |
| HTTP parser | Request line, headers, body, framing | Later. `Exchange` reads one raw line |
| Request handler | Validate a parsed request | Later |
| Router | Method and path to application logic | Later |
| Response writer | Status, headers, body, `Content-Length` | Later. `Exchange` writes one raw line |

`transport` knows sockets and the line protocol. `server` knows the accept loop and shutdown. Neither package imports an HTTP type. `net/http` is not part of this server.

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
Accept  ---- one connection ---- Exchange ---- Close
  ^                                            |
  +--------------------------------------------+
```

A client that has not finished its line is served before the next accepted client. Concurrent serving is a later milestone. The accept loop is the place that change lands. `Exchange` is the place an HTTP parser replaces the line protocol.

## Policies already fixed

These apply when the HTTP milestones arrive. They are recorded now so the TCP server does not grow a parser that contradicts them.

- Request line limit: 8 KiB. The Milestone 01 message limit is the same number, applied to one raw line.
- Headers: 32 KiB maximum, when header parsing exists.
- Request body: 1 MB maximum, when body reading exists.
- Concurrent connections: 100, when the server accepts concurrently.
- Idle timeout: 30 seconds. Milestone 01 uses 30 seconds as the read deadline on the one active connection.
- Chunked transfer encoding is unsupported until a chunked decoder exists. A chunked body must not be read as a `Content-Length` body.
- The sample body `{"message":"Hello from ForgeHTTP"}` is 34 bytes. A later response that sends it must use `Content-Length: 34`.

## Shutdown

Cancelling the context closes the listener and the active connection. `Accept` and a blocked `Read` return. `ListenAndServe` returns nil. Closing an already-closed listener or connection is ignored.

A temporary `Accept` error is logged and the loop continues. Any other `Accept` error stops the server.
