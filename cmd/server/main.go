package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var Version = "v0.0.0"
var Service = "bad-proxy"

var (
	ip           = getEnv("IP", "127.0.0.1")
	port         = getEnv("PORT", "8080")
	readTimeout  = getEnv("READ_TIMEOUT", "300")
	writeTimeout = getEnv("WRITE_TIMEOUT", "600")

	portCfg         = getEnv("PORT_CFG", "8070")
	readTimeoutCfg  = getEnv("READ_TIMEOUT_CFG", "30")
	writeTimeoutCfg = getEnv("WRITE_TIMEOUT_CFG", "60")

	backendURL = getEnv("BACKEND_URL", "http://localhost:8000")
)

type ProxyConfig struct {
	Latency        int     `json:"latency"`
	ConnectLatency int     `json:"connect_latency"`
	NoBackend      float64 `json:"no_backend"`
	Error500       float64 `json:"500"`
	Error400       float64 `json:"400"`
	Disconnect     float64 `json:"disconnect"`
	Corrupt        float64 `json:"corrupt"`
	WindowSize     int     `json:"error_window_size"`
	ForceErrors    bool    `json:"force_errors"`
}

type ErrorStats struct {
	Total           int                `json:"total_requests"`
	SuccessCount    int                `json:"success_count"`
	NoBackendCount  int                `json:"no_backend_count"`
	Error500Count   int                `json:"error_500_count"`
	Error400Count   int                `json:"error_400_count"`
	DisconnectCount int                `json:"disconnect_count"`
	CorruptCount    int                `json:"corrupt_count"`
	CurrentRates    map[string]float64 `json:"current_rates"`
	RecentErrors    []string           `json:"recent_errors"`
	RecentTotal     int                `json:"recent_total"`
}

var (
	config = ProxyConfig{
		Latency:        0,
		ConnectLatency: 0,
		NoBackend:      0,
		Error500:       0,
		Error400:       0,
		Disconnect:     0,
		Corrupt:        0,
		WindowSize:     100,
		ForceErrors:    true,
	}
	configMutex sync.RWMutex

	stats = ErrorStats{
		RecentErrors: make([]string, 100),
		CurrentRates: make(map[string]float64),
	}
	statsMutex sync.RWMutex
)

func main() {
	readTimeoutInt, err := strconv.Atoi(readTimeout)
	if err != nil {
		fmt.Println("Parsing error, READ_TIMEOUT must be an integer of seconds.")
		os.Exit(1)
	}

	writeTimeoutInt, err := strconv.Atoi(writeTimeout)
	if err != nil {
		fmt.Println("Parsing error, WRITE_TIMEOUT must be an integer of seconds.")
		os.Exit(1)
	}

	readTimeoutCfgInt, err := strconv.Atoi(readTimeoutCfg)
	if err != nil {
		fmt.Println("Parsing error, READ_TIMEOUT_CFG must be an integer of seconds.")
		os.Exit(1)
	}

	writeTimeoutCfgInt, err := strconv.Atoi(writeTimeoutCfg)
	if err != nil {
		fmt.Println("Parsing error, WRITE_TIMEOUT_CFG must be an integer of seconds.")
		os.Exit(1)
	}

	zapCfg := zap.NewProductionConfig()
	baseLogger, err := zapCfg.Build()
	if err != nil {
		fmt.Printf("Can not build logger: %s\n", err.Error())
		os.Exit(1)
	}

	logger := baseLogger.With(zap.String("app", Service), zap.String("app_version", Version))
	logger.Info("Starting Bad Proxy Server",
		zap.String("port", port),
		zap.String("ip", ip),
		zap.String("backend_url", backendURL),
	)

	r := gin.New()
	r.Use(ginzap.Ginzap(logger, time.RFC3339, true))

	r.Any("/*path", func(c *gin.Context) {
		proxyRequest(c, logger)
	})

	rCfg := gin.New()
	rCfg.Use(ginzap.Ginzap(logger, time.RFC3339, true))

	rCfg.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":      "ok",
			"version":     Version,
			"port":        port,
			"ip":          ip,
			"backend_url": backendURL,
		})
	})

	rCfg.GET("/config", func(c *gin.Context) {
		configMutex.RLock()
		currentConfig := config
		configMutex.RUnlock()

		statsMutex.RLock()
		currentStats := stats
		statsMutex.RUnlock()

		c.JSON(http.StatusOK, gin.H{
			"config": currentConfig,
			"stats":  currentStats,
		})
	})

	rCfg.GET("/reset-stats", func(c *gin.Context) {
		statsMutex.Lock()
		stats = ErrorStats{
			RecentErrors: make([]string, config.WindowSize),
			CurrentRates: make(map[string]float64),
		}
		statsMutex.Unlock()

		c.JSON(http.StatusOK, gin.H{
			"status": "Statistics reset successful",
		})
	})

	rCfg.POST("/config", func(c *gin.Context) {
		var newConfig ProxyConfig
		if err := c.ShouldBindJSON(&newConfig); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid configuration format"})
			return
		}

		if newConfig.WindowSize <= 0 {
			newConfig.WindowSize = 100
		}

		oldWindowSize := config.WindowSize

		configMutex.Lock()
		config = newConfig
		configMutex.Unlock()

		if oldWindowSize != newConfig.WindowSize {
			statsMutex.Lock()
			stats.RecentErrors = make([]string, newConfig.WindowSize)
			statsMutex.Unlock()
		}

		logger.Info("Proxy configuration updated",
			zap.Int("latency", newConfig.Latency),
			zap.Int("connect_latency", newConfig.ConnectLatency),
			zap.Float64("no_backend", newConfig.NoBackend),
			zap.Float64("500", newConfig.Error500),
			zap.Float64("400", newConfig.Error400),
			zap.Float64("disconnect", newConfig.Disconnect),
			zap.Float64("corrupt", newConfig.Corrupt),
			zap.Int("window_size", newConfig.WindowSize),
		)

		c.JSON(http.StatusOK, gin.H{"status": "configuration updated"})
	})

	go func() {
		logger.Info("Starting Bad Proxy Configuration Server",
			zap.String("version", Version),
			zap.String("port", portCfg),
		)

		sCfg := &http.Server{
			Addr:           ip + ":" + portCfg,
			Handler:        rCfg,
			ReadTimeout:    time.Duration(readTimeoutCfgInt) * time.Second,
			WriteTimeout:   time.Duration(writeTimeoutCfgInt) * time.Second,
			MaxHeaderBytes: 1 << 20,
		}

		err = sCfg.ListenAndServe()
		if err != nil {
			logger.Fatal("unable to start the Bad Proxy Configuration Server", zap.Error(err))
		}
	}()

	s := &http.Server{
		Addr:           ip + ":" + port,
		Handler:        r,
		ReadTimeout:    time.Duration(readTimeoutInt) * time.Second,
		WriteTimeout:   time.Duration(writeTimeoutInt) * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	err = s.ListenAndServe()
	if err != nil {
		logger.Fatal(err.Error())
	}
}

func proxyRequest(c *gin.Context, logger *zap.Logger) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodPost {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Only GET and POST methods are supported"})
		return
	}

	configMutex.RLock()
	latency := config.Latency
	connectLatency := config.ConnectLatency
	noBackendProb := config.NoBackend
	error500Prob := config.Error500
	error400Prob := config.Error400
	disconnectProb := config.Disconnect
	corruptProb := config.Corrupt
	forceErrors := config.ForceErrors
	windowSize := config.WindowSize
	configMutex.RUnlock()

	statsMutex.Lock()
	stats.Total++
	recentPos := stats.Total % windowSize
	if recentPos >= len(stats.RecentErrors) {
		recentPos = len(stats.RecentErrors) - 1
	}

	var errorType string
	applyError := false

	if forceErrors {
		successiveNoErrors := countSuccessiveNoErrors(stats.RecentErrors)
		maxAllowedSuccessiveSuccess := calculateMaxAllowedSuccessive(disconnectProb, error500Prob, error400Prob, noBackendProb, corruptProb)

		if successiveNoErrors >= maxAllowedSuccessiveSuccess && maxAllowedSuccessiveSuccess > 0 {
			applyError = true
			errorType = selectForcedErrorType(disconnectProb, error500Prob, error400Prob, noBackendProb, corruptProb)
		}
	}

	if !applyError {
		randomVal := rand.Float64()
		cumulativeProb := 0.0

		if disconnectProb > 0 {
			cumulativeProb += disconnectProb
			if randomVal < cumulativeProb {
				errorType = "disconnect"
				applyError = true
			}
		}

		if !applyError && error500Prob > 0 {
			cumulativeProb += error500Prob
			if randomVal < cumulativeProb {
				errorType = "error500"
				applyError = true
			}
		}

		if !applyError && error400Prob > 0 {
			cumulativeProb += error400Prob
			if randomVal < cumulativeProb {
				errorType = "error400"
				applyError = true
			}
		}

		if !applyError && noBackendProb > 0 {
			cumulativeProb += noBackendProb
			if randomVal < cumulativeProb {
				errorType = "no_backend"
				applyError = true
			}
		}

		if !applyError && corruptProb > 0 {
			cumulativeProb += corruptProb
			if randomVal < cumulativeProb {
				errorType = "corrupt"
				applyError = true
			}
		}
	}

	stats.RecentErrors[recentPos] = errorType
	updateErrorStats(errorType, &stats)
	updateErrorRates(&stats, windowSize)
	statsMutex.Unlock()

	if connectLatency > 0 {
		time.Sleep(time.Duration(connectLatency) * time.Second)
	}

	if errorType == "disconnect" {
		logger.Info("Disconnecting based on configured probability",
			zap.Int("request_num", stats.Total),
			zap.Float64("disconnect", disconnectProb))

		hijacker, ok := c.Writer.(http.Hijacker)
		if !ok {
			logger.Error("Response writer does not support hijacking")
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		conn, _, err := hijacker.Hijack()
		if err != nil {
			logger.Error("Failed to hijack connection", zap.Error(err))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}

		err = conn.Close()
		if err != nil {
			return
		}
		c.Abort()
		return
	}

	if errorType == "no_backend" {
		logger.Info("Preventing backend request based on configured probability",
			zap.Int("request_num", stats.Total),
			zap.Float64("no_backend", noBackendProb))

		time.Sleep(time.Duration(latency) * time.Second)
		c.JSON(http.StatusOK, gin.H{"message": "Response generated by Bad-Proxy without reaching backend"})
		return
	}

	if errorType == "error400" {
		logger.Info("Returning 400 Bad Request based on configured probability",
			zap.Int("request_num", stats.Total),
			zap.Float64("error400", error400Prob))

		time.Sleep(time.Duration(latency) * time.Second)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bad request error generated by Bad-Proxy"})
		return
	}

	if errorType == "error500" {
		logger.Info("Returning 500 Internal Server Error based on configured probability",
			zap.Int("request_num", stats.Total),
			zap.Float64("error500", error500Prob))

		time.Sleep(time.Duration(latency) * time.Second)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error generated by Bad-Proxy"})
		return
	}

	if latency > 0 && connectLatency == 0 {
		time.Sleep(time.Duration(latency) * time.Second)
	}

	targetURL := backendURL + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	var requestBody []byte
	if c.Request.Body != nil {
		var err error
		requestBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			logger.Error("Failed to read request body", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
			return
		}
	}

	req, err := http.NewRequest(c.Request.Method, targetURL, bytes.NewBuffer(requestBody))
	if err != nil {
		logger.Error("Failed to create proxy request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create proxy request"})
		return
	}

	for name, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Failed to execute proxy request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to execute proxy request"})
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logger.Error("Failed to close response body", zap.Error(err))
		}
	}(resp.Body)

	for name, values := range resp.Header {
		for _, value := range values {
			c.Header(name, value)
		}
	}

	c.Status(resp.StatusCode)

	if errorType == "corrupt" {
		logger.Info("Corrupting response based on configured probability",
			zap.Int("request_num", stats.Total),
			zap.Float64("corrupt", corruptProb))

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Error("Failed to read response body for corruption", zap.Error(err))
			c.Status(http.StatusInternalServerError)
			return
		}

		originalLength := len(responseBody)
		if originalLength > 0 {
			minLength := int(float64(originalLength) * 0.1)
			maxLength := int(float64(originalLength) * 0.9)

			if minLength < 1 {
				minLength = 1
			}

			if maxLength <= minLength {
				maxLength = minLength + 1
			}

			truncatedLength := minLength
			if maxLength > minLength {
				truncatedLength = minLength + rand.IntN(maxLength-minLength)
			}

			logger.Info("Truncating response",
				zap.Int("original_length", originalLength),
				zap.Int("truncated_length", truncatedLength))

			_, err = c.Writer.Write(responseBody[:truncatedLength])
			if err != nil {
				logger.Error("Failed to write corrupted response", zap.Error(err))
			}
		}
	} else {
		_, err = io.Copy(c.Writer, resp.Body)
		if err != nil {
			logger.Error("Failed to copy response body", zap.Error(err))
		}
	}
}

func updateErrorStats(errorType string, stats *ErrorStats) {
	switch errorType {
	case "disconnect":
		stats.DisconnectCount++
	case "error500":
		stats.Error500Count++
	case "error400":
		stats.Error400Count++
	case "no_backend":
		stats.NoBackendCount++
	case "corrupt":
		stats.CorruptCount++
	case "":
		stats.SuccessCount++
	}
}

func updateErrorRates(stats *ErrorStats, windowSize int) {
	recentCount := stats.Total
	if recentCount > windowSize {
		recentCount = windowSize
	}

	stats.RecentTotal = recentCount

	if recentCount == 0 {
		return
	}

	disconnectCount := 0
	error500Count := 0
	error400Count := 0
	noBackendCount := 0
	corruptCount := 0

	for _, errType := range stats.RecentErrors {
		switch errType {
		case "disconnect":
			disconnectCount++
		case "error500":
			error500Count++
		case "error400":
			error400Count++
		case "no_backend":
			noBackendCount++
		case "corrupt":
			corruptCount++
		}
	}

	stats.CurrentRates["disconnect"] = float64(disconnectCount) / float64(recentCount)
	stats.CurrentRates["500"] = float64(error500Count) / float64(recentCount)
	stats.CurrentRates["400"] = float64(error400Count) / float64(recentCount)
	stats.CurrentRates["no_backend"] = float64(noBackendCount) / float64(recentCount)
	stats.CurrentRates["corrupt"] = float64(corruptCount) / float64(recentCount)
}

func countSuccessiveNoErrors(recentErrors []string) int {
	count := 0
	for i := len(recentErrors) - 1; i >= 0; i-- {
		if recentErrors[i] == "" {
			count++
		} else if recentErrors[i] != "" {
			break
		}
	}
	return count
}

func calculateMaxAllowedSuccessive(disconnectProb, error500Prob, error400Prob, noBackendProb, corruptProb float64) int {
	totalErrorProb := disconnectProb + error500Prob + error400Prob + noBackendProb + corruptProb

	if totalErrorProb <= 0 {
		return 0
	}

	if totalErrorProb >= 1.0 {
		return 1
	}

	maxSuccessive := int(5.0 / totalErrorProb)
	if maxSuccessive < 5 {
		return 5
	}

	if maxSuccessive > 20 {
		return 20
	}

	return maxSuccessive
}

func selectForcedErrorType(disconnectProb, error500Prob, error400Prob, noBackendProb, corruptProb float64) string {
	totalProb := disconnectProb + error500Prob + error400Prob + noBackendProb + corruptProb
	if totalProb <= 0 {
		return ""
	}

	errorTypes := []string{"disconnect", "error500", "error400", "no_backend", "corrupt"}
	probabilities := []float64{disconnectProb, error500Prob, error400Prob, noBackendProb, corruptProb}

	randomVal := rand.Float64() * totalProb
	cumulativeProb := 0.0

	for i, prob := range probabilities {
		if prob <= 0 {
			continue
		}

		cumulativeProb += prob
		if randomVal < cumulativeProb {
			return errorTypes[i]
		}
	}

	for i := len(probabilities) - 1; i >= 0; i-- {
		if probabilities[i] > 0 {
			return errorTypes[i]
		}
	}

	return ""
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}

	return value
}
