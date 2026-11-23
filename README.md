# ForgeHTTP

Phase 6 keeps an HTTP/1.1 connection open for the next request. Each connection still runs on its own goroutine, and the accept loop allows 100 connections at once.

`go run ./cmd/forgehttp` registers `GET /` and `GET /hello`. A matching request receives:

```
HTTP/1.1 200 OK
Content-Type: application/json
Connection: keep-alive
Content-Length: 34

{"message":"Hello from ForgeHTTP"}
```

`Content-Length` is the number of body bytes. HTTP/1.1 stays open unless the request says `Connection: close`. HTTP/1.0 closes unless the request says `Connection: keep-alive`. A connection that sends nothing for 30 seconds is closed. A request that stalls after the first byte is limited by the read deadline and answered with 408. An unknown path is 404. A known path with another method is 405. A malformed request receives an error status such as 400, 413, 431, 501, or 505, and that connection closes. Two clients can be served at the same time. Past 100 open connections, further clients wait in the listen queue until one finishes. See [docs/architecture.md](docs/architecture.md).

The small REST API is `examples/rest-api`:

```
go run ./examples/rest-api
```

```
curl http://127.0.0.1:8080/api/users
curl -H "Content-Type: application/json" -d "{\"name\":\"Ada\"}" http://127.0.0.1:8080/api/users
```

A request line is limited to 8 KiB, the header block to 32 KiB, and a `Content-Length` body to 1 MB. `Transfer-Encoding` is rejected and its body is not read. A chunked body is never treated as a `Content-Length` body.

## Run

```
go run ./cmd/forgehttp
```

```
go run ./cmd/forgehttp -addr 127.0.0.1:8080
```

Ctrl+C stops new connections, finishes a response already started, and closes connections that are waiting for a request.

## Try it

```
curl -v http://127.0.0.1:8080/hello
```

The body is `{"message":"Hello from ForgeHTTP"}`.

A raw client can send the bytes itself:

```powershell
$client = New-Object System.Net.Sockets.TcpClient("127.0.0.1", 8080)
$stream = $client.GetStream()
$writer = New-Object System.IO.StreamWriter($stream)
$writer.NewLine = "`r`n"
$writer.WriteLine("GET /hello HTTP/1.1")
$writer.WriteLine("Host: localhost:8080")
$writer.WriteLine("")
$writer.Flush()
$reader = New-Object System.IO.StreamReader($stream)
$reader.ReadToEnd()
$client.Close()
```

Several clients can be connected at once. One connection can send another request after it has read the response. The next response is not written first.

## Test

```
go test ./...
go test -race ./...
```

`make test` and `make race` run the same commands when `make` is available.
