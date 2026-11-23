# ForgeHTTP

Milestone 01 is a TCP server. It listens on `127.0.0.1:8080`, accepts one client at a time, reads one newline-terminated message, writes one newline-terminated reply, and closes the connection.

`Hello Server` receives `Hello Client`. Any other line is echoed back as `Echo: <line>`. A message with no newline inside 8 KiB is rejected and the connection is closed.

This process does not speak HTTP. Parsing, routing, and a goroutine per connection come in later milestones. See [docs/architecture.md](docs/architecture.md).

## Run

```
go run ./cmd/forgehttp
```

```
go run ./cmd/forgehttp -addr 127.0.0.1:8080
```

Ctrl+C cancels the process. The listener closes, the active connection closes, and `ListenAndServe` returns.

## Try it

With the server running, from PowerShell:

```powershell
$client = New-Object System.Net.Sockets.TcpClient("127.0.0.1", 8080)
$stream = $client.GetStream()
$writer = New-Object System.IO.StreamWriter($stream)
$writer.NewLine = "`n"
$writer.WriteLine("Hello Server")
$writer.Flush()
$reader = New-Object System.IO.StreamReader($stream)
$reader.ReadLine()
$client.Close()
```

The read prints `Hello Client`. Close that client before opening another: the accept loop handles one connection to completion, so a client that stays open is the only client being served.

## Test

```
go test ./...
go test -race ./...
```

`make test` and `make race` run the same commands when `make` is available.

## What the process is doing

`net.Listen` creates a TCP socket bound to `127.0.0.1:8080`. `Accept` blocks until a client completes the handshake. The accepted `net.Conn` is a byte stream: one `Read` may contain part of a line, a whole line, or more than a line. A `bufio.Reader` collects bytes until `\n` or the 8 KiB limit. `\r\n` and `\n` both end the message.

The accept loop stays on the `ListenAndServe` goroutine. A second goroutine only waits for cancellation, then closes the listener and the active connection so `Accept` and `Read` both unblock. Each connection has a 30 second read deadline and a 10 second write deadline, so a silent client cannot hold that single loop until the process is killed.

Replies are written in a loop until every byte is accepted. A short `Write` does not drop the tail of the response.
