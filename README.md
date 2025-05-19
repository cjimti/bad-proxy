# Bad Proxy

Bad Proxy is a fault-injection proxy server designed for testing API clients under adverse conditions. It allows you to simulate various types of API failures to ensure your clients can gracefully handle problematic API behaviors.

## Overview

Bad Proxy sits between your client and backend server, allowing you to configure different failure modes for testing resilience in your API clients. It provides a simple HTTP API to configure these failure modes dynamically during testing.

## Features

- **Artificial Latency**: Add configurable delay to responses
- **Error Injection**: Return 400 or 500 errors based on probability
- **Connection Termination**: Abruptly close connections to test reconnection logic
- **Response Corruption**: Return truncated responses to test partial data handling

## Configuration

The proxy runs two servers:
- Main proxy server (default: port 8080)
- Configuration server (default: port 8070)

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| IP | IP address to bind to | 127.0.0.1 |
| PORT | Main proxy port | 8080 |
| PORT_CFG | Configuration port | 8070 |
| READ_TIMEOUT | Proxy read timeout (seconds) | 300 |
| WRITE_TIMEOUT | Proxy write timeout (seconds) | 600 |
| READ_TIMEOUT_CFG | Config API read timeout (seconds) | 30 |
| WRITE_TIMEOUT_CFG | Config API write timeout (seconds) | 60 |
| BACKEND_URL | URL of the backend service to proxy | http://localhost:8000 |

## API

### Status Check

```
GET /status
```

Returns status information including version and configuration.

### Get Current Configuration

```
GET /config
```

Returns the current proxy configuration.

### Update Configuration

```
POST /config
```

Updates the proxy behavior configuration.

**Request Body:**

```json
{
  "latency": 2,        // Added delay in seconds
  "500": 0.1,          // Probability of returning 500 errors (0.0-1.0)
  "400": 0.05,         // Probability of returning 400 errors (0.0-1.0)
  "disconnect": 0.02,  // Probability of disconnecting (0.0-1.0)
  "corrupt": 0.05      // Probability of corrupting response (0.0-1.0)
}
```

## Docker Usage

```bash
# Build the container
docker build -t bad-proxy .

# Run the container
docker run -p 8080:8080 -p 8070:8070 -e BACKEND_URL=http://your-api-server.com bad-proxy
```

# TXN2 Docker Image

- `txn2/bad-proxy:1.0.0`

## Use Cases

- Testing client retry logic
- Evaluating timeout handling
- Verifying error handling mechanisms
- Stress testing under adverse network conditions
- Simulating slow or unreliable API dependencies

## Example: Testing a Retry Mechanism

1. Start Bad Proxy pointing to your API:
   ```bash
   export BACKEND_URL=http://api.example.com
   go run cmd/server/main.go
   ```

2. Configure a 50% probability of 500 errors:
   ```bash
   curl -X POST http://localhost:8070/config \
     -H "Content-Type: application/json" \
     -d '{"latency": 0, "500": 0.5, "400": 0, "disconnect": 0, "corrupt": 0}'
   ```

3. Point your client to the proxy (http://localhost:8080) and observe how it handles the errors

## Building From Source

```bash
# Clone the repository
git clone https://github.com/cjimti/bad-proxy.git
cd bad-proxy

# Build
go build -o bad-proxy cmd/server/main.go

# Run
./bad-proxy
```

## License

[MIT License](LICENSE)
