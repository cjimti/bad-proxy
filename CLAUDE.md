# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Bad Proxy is a fault-injection proxy server written in Go for testing API client resilience under adverse conditions. It sits between a client and backend server, allowing dynamic configuration of failure modes (errors, latency, disconnections, response corruption).

## Architecture

### Single-File Design
The entire application is contained in `cmd/server/main.go` (~620 lines). This is intentional for simplicity.

### Dual Server Architecture
The application runs two HTTP servers concurrently:
1. **Main Proxy Server** (port 8080): Handles all proxied requests with configured fault injection
2. **Configuration Server** (port 8070): REST API for runtime configuration and statistics

### Core Components

**ProxyConfig** (`main.go:35-45`): Configuration structure for fault injection behavior
- Latency settings (response and connection)
- Error probabilities (500, 400, disconnect, corrupt, no_backend)
- Window size for statistics tracking
- Force errors flag to prevent unlikely success streaks

**ErrorStats** (`main.go:47-58`): Real-time statistics tracking
- Total and per-type error counts
- Sliding window-based current error rates
- Recent error history array for streak detection

**Probability System** (`proxyRequest` function, `main.go:239-488`):
- Uses true random probability with cumulative distribution
- Forced error mechanism (`main.go:267-275`) prevents statistically unlikely success streaks
- Error selection uses weighted random based on configured probabilities

### Concurrency Model
- Uses `sync.RWMutex` for config and stats to allow concurrent reads with exclusive writes
- Config server runs in a goroutine (`main.go:205-223`)
- Main proxy server runs in main goroutine

## Development Commands

### Building
```bash
# Build locally
go build -o bad-proxy cmd/server/main.go

# Build with version
go build -ldflags "-X main.Version=v1.4.0" -o bad-proxy cmd/server/main.go
```

### Running
```bash
# Run directly with go
go run cmd/server/main.go

# Run with custom configuration
BACKEND_URL=http://api.example.com PORT=9090 PORT_CFG=9070 go run cmd/server/main.go
```

### Docker
```bash
# Build Docker image (from PACKAGING.md)
export BP_VERSION=1.4.0
docker build --build-arg version=${BP_VERSION} -t txn2/bad-proxy:${BP_VERSION} .

# Run container
docker run -p 8080:8080 -p 8070:8070 -e BACKEND_URL=http://backend:8000 txn2/bad-proxy:${BP_VERSION}
```

### Testing the Proxy
```bash
# Check status
curl http://localhost:8070/status

# Get current config and stats
curl http://localhost:8070/config

# Update configuration (inject 50% 500 errors)
curl -X POST http://localhost:8070/config \
  -H "Content-Type: application/json" \
  -d '{"latency": 0, "connect_latency": 0, "500": 0.5, "400": 0, "disconnect": 0, "corrupt": 0, "no_backend": 0, "error_window_size": 100, "force_errors": true}'

# Reset statistics
curl http://localhost:8070/reset-stats

# Send proxied request
curl http://localhost:8080/any/path
```

## Configuration

### Environment Variables
- `IP`: Bind address (default: 127.0.0.1)
- `PORT`: Main proxy port (default: 8080)
- `PORT_CFG`: Configuration API port (default: 8070)
- `READ_TIMEOUT`: Proxy read timeout in seconds (default: 300)
- `WRITE_TIMEOUT`: Proxy write timeout in seconds (default: 600)
- `READ_TIMEOUT_CFG`: Config API read timeout in seconds (default: 30)
- `WRITE_TIMEOUT_CFG`: Config API write timeout in seconds (default: 60)
- `BACKEND_URL`: Backend service URL to proxy (default: http://localhost:8000)

### Version Management
Version is set via `-ldflags` during build: `-X main.Version=vX.Y.Z`

## Key Implementation Details

### Error Injection Priority
Error types are evaluated in order (`main.go:281-319`):
1. Disconnect (hijacks connection and closes immediately)
2. 500 errors (returns before proxying)
3. 400 errors (returns before proxying)
4. No backend (returns mock response without proxying)
5. Corrupt (proxies request but truncates response body by 10-90%)

### Forced Error System
- `calculateMaxAllowedSuccessive` (`main.go:559-580`): Calculates max allowed consecutive successes based on total error probability
- `countSuccessiveNoErrors` (`main.go:547-557`): Counts recent consecutive successes from the end of the sliding window
- `selectForcedErrorType` (`main.go:582-612`): Weighted random selection of error type when forcing errors

### Statistics Window
- Circular buffer implementation using modulo arithmetic (`main.go:258-262`)
- Window size is configurable and affects forced error calculations
- Stats are updated atomically under lock for each request

## Dependencies
- `github.com/gin-gonic/gin`: HTTP router and server framework
- `go.uber.org/zap`: Structured logging
- `github.com/gin-contrib/zap`: Gin middleware for zap logging

## Notes
- No test files exist in the codebase
- Proxy only supports GET and POST methods (`main.go:240-243`)
- All request headers are forwarded to backend (`main.go:415-419`)
- All response headers are forwarded to client (`main.go:435-439`)
