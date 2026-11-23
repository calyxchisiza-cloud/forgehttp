# ForgeHTTP architecture

Phase 1 accepted one TCP connection at a time. Phase 2 parses one HTTP/1 request. Phase 3 serializes an HTTP/1.1 response. Phase 4 routes that request by exact method and path. Phase 5 serves each connection on its own goroutine, up to 100 at once.

## Components

| Component | Responsibility | Now |
| --- | --- | --- |
| TCP listener | Bind a TCP socket and accept connections | `transport.Listen`, `server.ListenAndServe` |
| Connection manager | Deadlines, open connections, the connection limit, shutdown | One goroutine per connection, limit 100, context shutdown |
| HTTP parser | Request line, headers, body, framing | `http1.ReadRequest` |
| Request handler | Validate a parsed request beyond framing | Later |
| Router | Method and path to application logic | `router.Router`, exact paths only |
| Response writer | Status, headers, body, `Content-Length` | `Response.Write` |

`transport` opens sockets. `protocol/http1` reads requests and writes responses. `router` matches an exact method and path. `server` owns the accept loop, deadlines, and shutdown. `net/http` is not part of this server.

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
Accept -- slot available -- goroutine -- ReadRequest -- Router -- Response.Write -- Close
  ^                                                                              |
  +------------------------------------------------------------------------------+
```

The accept loop takes one slot from a limit of 100 before `Accept`. That slot stays with the connection goroutine until the connection closes. At the limit, `Accept` waits, and further clients remain in the kernel listen queue. That is the backpressure. A connection reads one request, writes one response, and closes. It does not pipeline another response ahead of that write. Handlers that share data must synchronize it themselves. The user store in `examples/rest-api` does that with a mutex.

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

- Idle timeout on a kept-alive connection. The read deadline is still 30 seconds for the one request on a connection.
- Keep-alive and more than one request per connection.
- Chunked transfer encoding stays unsupported until a chunked decoder exists.
- `{"message":"Hello from ForgeHTTP"}` is 34 bytes. The phase 3 response sends `Content-Length: 34`.

## Routing

- `Handle` registers one uppercase method and one exact path, such as `GET` and `/api/users`.
- The query string is not part of the match. `/api/users?x=1` uses the path `/api/users`.
- An unknown path is 404. A known path with another method is 405, and the response includes `Allow`.
- The server sets the request context from its own context before the handler runs.
- Route parameters are a later phase.

## Response rules

- The status line is `HTTP/1.1`, a code from 100 to 599, and a fixed reason phrase.
- `Content-Length` is `len(Body)`. A length supplied by the caller is not written.
- `Connection` is `close`.
- `Transfer-Encoding` is rejected.
- Header names must be tokens. Header values must not contain CR, LF, or other control characters, so a value cannot start another header.

## Shutdown

Cancelling the context closes the listener and every open connection. `Accept` and a blocked `Read` return. `ListenAndServe` waits for the connection goroutines and then returns nil. Closing an already-closed listener or connection is ignored.

A temporary `Accept` error is logged and the loop continues. Any other `Accept` error stops the server.
