# Bad Proxy

Bad Proxy is a fault-injection proxy server designed for testing API clients under adverse conditions. It allows you to simulate various types of API failures to ensure your clients can gracefully handle problematic API behaviors.

## Overview

Bad Proxy sits between your client and backend server, allowing you to configure different failure modes for testing resilience in your API clients. It provides a simple HTTP API to configure these failure modes dynamically during testing.

## Features

- **Artificial Latency**: Add configurable delay to responses
- **Connect Latency**: Simulate initial connection delay before responding
- **No Backend Mode**: Return responses without contacting the backend server
- **Error Injection**: Return 400 or 500 errors based on probability
- **Connection Termination**: Abruptly close connections to test reconnection logic
- **Response Corruption**: Return truncated responses to test partial data handling
- **Reliable Error Distribution**: True random probability with forced errors to prevent unlikely streaks
- **Detailed Statistics**: Track error rates and distribution in real-time

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

### Get Current Configuration and Stats

```
GET /config
```

Returns:
- Current proxy configuration
- Detailed error statistics
- Current error rates
- Recent error history
- Total request count

### Reset Statistics

```
GET /reset-stats
```

Resets all error statistics without changing the configuration.

### Update Configuration

```
POST /config
```

Updates the proxy behavior configuration.

**Request Body:**

```plain
{
  "latency": 2,                // Added delay in seconds after connection
  "connect_latency": 5,        // Initial connection delay in seconds
  "no_backend": 0.1,           // Probability of not forwarding to backend (0.0-1.0)
  "500": 0.1,                  // Probability of returning 500 errors (0.0-1.0)
  "400": 0.05,                 // Probability of returning 400 errors (0.0-1.0)
  "disconnect": 0.02,          // Probability of disconnecting (0.0-1.0)
  "corrupt": 0.05,             // Probability of corrupting response (0.0-1.0)
  "error_window_size": 100,    // Size of the sliding window for statistics
  "force_errors": true         // Force errors after long success streaks
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

- `txn2/bad-proxy:1.4.0`

## Probability System

Bad Proxy uses a rock-solid probability system that ensures errors occur at the configured rates:

### True Random Probability with Safeguards

- Each request generates a random number that is directly compared to configured error thresholds
- Errors are triggered immediately when the random value falls below the threshold
- The system prevents unlikely streaks by forcing errors when too many successes occur in a row
- Error types are selected based on their relative probabilities

### Detailed Statistics

The `/config` endpoint provides comprehensive statistics:
- Total requests processed
- Success and error counts for each error type
- Current error rates across the configured window size
- Recent error history showing the pattern of errors

### Forced Error Prevention

The `force_errors` setting (enabled by default) ensures you won't see long streaks of successes when errors should be occurring:

- Automatically calculates the maximum reasonable streak length based on configured error rates
- Forces errors after the maximum reasonable streak length is exceeded
- Distributes forced errors according to the configured probability ratios
- Can be disabled if you want truly random behavior with possible streaks

## Use Cases

- Testing client retry logic
- Evaluating timeout handling
- Verifying error handling mechanisms
- Stress testing under adverse network conditions
- Simulating slow or unreliable API dependencies
- Testing behavior with high-latency initial connections
- Validating client behavior when backend services are unavailable

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
     -d '{"latency": 0, "connect_latency": 2, "500": 0.5, "400": 0, "disconnect": 0, "corrupt": 0, "error_window_size": 100, "force_errors": true}'
   ```

3. Point your client to the proxy (http://localhost:8080) and observe how it handles the errors

4. Check the actual error distribution:
   ```bash
   curl http://localhost:8070/config
   ```

## Example: Testing Multiple Error Types

1. Configure multiple error types with different probabilities:
   ```bash
   curl -X POST http://localhost:8070/config \
     -H "Content-Type: application/json" \
     -d '{"latency": 1, "connect_latency": 0, "no_backend": 0.1, "500": 0.2, "400": 0.1, "disconnect": 0.1, "corrupt": 0.1, "error_window_size": 100, "force_errors": true}'
   ```

2. Your client should experience errors with a combined probability of 60% (10% no_backend + 20% 500 errors + 10% 400 errors + 10% disconnects + 10% corrupted responses)

3. Reset statistics to start a fresh test:
   ```bash
   curl http://localhost:8070/reset-stats
   ```

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