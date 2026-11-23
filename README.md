# ForgeHTTP

Phase 2 reads HTTP/1 requests. The server still accepts one TCP connection at a time on `127.0.0.1:8080`. For each connection it parses one request and writes a plain-text dump of that parse, then closes.

The dump is there so `curl` and a browser can finish the exchange. The response writer, routing, and a goroutine per connection are later phases. See [docs/architecture.md](docs/architecture.md).

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

The body is the parsed request:

```
GET /hello HTTP/1.1
Host: 127.0.0.1:8080
...
```

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
