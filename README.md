# ForgeHTTP

Phase 3 writes HTTP/1.1 responses. The server still accepts one TCP connection at a time on `127.0.0.1:8080`. A valid request receives:

```
HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 34
Connection: close

{"message":"Hello from ForgeHTTP"}
```

`Content-Length` is the number of body bytes. A malformed request receives an error status such as 400, 413, 431, 501, or 505. Routing and a goroutine per connection are later phases. See [docs/architecture.md](docs/architecture.md).

A request line is limited to 8 KiB, the header block to 32 KiB, and a `Content-Length` body to 1 MB. `Transfer-Encoding` is rejected and its body is not read. A chunked body is never treated as a `Content-Length` body.

## Run

```
go run ./cmd/forgehttp
```

```
go run ./cmd/forgehttp -addr 127.0.0.1:8080
```

Ctrl+C closes the listener and the active connection.

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

Close one client before the next. The accept loop serves one connection to completion.

## Test

```
go test ./...
go test -race ./...
```

`make test` and `make race` run the same commands when `make` is available.
