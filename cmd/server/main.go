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
	Latency         int     `json:"latency"`
	ConnectLatency  int     `json:"connect_latency"`
	NoBackend       float64 `json:"no_backend"`
	Error500        float64 `json:"500"`
	Error400        float64 `json:"400"`
	Disconnect      float64 `json:"disconnect"`
	Corrupt         float64 `json:"corrupt"`
	ErrorWindowSize int     `json:"error_window_size"`
}

type ErrorTracker struct {
	WindowSize     int
	RequestCounter int
	ErrorBuckets   map[string][]bool
}

var (
	config = ProxyConfig{
		Latency:         0,
		ConnectLatency:  0,
		NoBackend:       0,
		Error500:        0,
		Error400:        0,
		Disconnect:      0,
		Corrupt:         0,
		ErrorWindowSize: 100,
	}
	configMutex sync.RWMutex

	errorTracker = ErrorTracker{
		WindowSize:     100,
		RequestCounter: 0,
		ErrorBuckets: map[string][]bool{
			"error500":   make([]bool, 100),
			"error400":   make([]bool, 100),
			"disconnect": make([]bool, 100),
			"corrupt":    make([]bool, 100),
			"no_backend": make([]bool, 100),
		},
	}
	trackerMutex sync.RWMutex
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

		trackerMutex.RLock()
		errorRates := calculateCurrentErrorRates()
		windowSize := errorTracker.WindowSize
		requestsProcessed := errorTracker.RequestCounter
		trackerMutex.RUnlock()

		c.JSON(http.StatusOK, gin.H{
			"config":             currentConfig,
			"actual_rates":       errorRates,
			"window_size":        windowSize,
			"requests_processed": requestsProcessed,
		})
	})

	rCfg.POST("/config", func(c *gin.Context) {
		var newConfig ProxyConfig
		if err := c.ShouldBindJSON(&newConfig); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid configuration format"})
			return
		}

		if newConfig.ErrorWindowSize <= 0 {
			newConfig.ErrorWindowSize = 100
		}

		configMutex.Lock()
		config = newConfig
		configMutex.Unlock()

		trackerMutex.Lock()
		if errorTracker.WindowSize != newConfig.ErrorWindowSize {
			errorTracker.WindowSize = newConfig.ErrorWindowSize
			for errorType := range errorTracker.ErrorBuckets {
				errorTracker.ErrorBuckets[errorType] = make([]bool, newConfig.ErrorWindowSize)
			}
		}
		resetErrorTrackers()
		trackerMutex.Unlock()

		logger.Info("Proxy configuration updated",
			zap.Int("latency", newConfig.Latency),
			zap.Int("connect_latency", newConfig.ConnectLatency),
			zap.Float64("no_backend", newConfig.NoBackend),
			zap.Float64("500", newConfig.Error500),
			zap.Float64("400", newConfig.Error400),
			zap.Float64("disconnect", newConfig.Disconnect),
			zap.Float64("corrupt", newConfig.Corrupt),
			zap.Int("error_window_size", newConfig.ErrorWindowSize),
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
	noBackend := config.NoBackend
	error500 := config.Error500
	error400 := config.Error400
	disconnect := config.Disconnect
	corrupt := config.Corrupt
	configMutex.RUnlock()

	trackerMutex.Lock()
	currentIndex := errorTracker.RequestCounter % errorTracker.WindowSize
	errorTracker.RequestCounter++

	shouldNoBackend := shouldTriggerError("no_backend", noBackend, currentIndex)
	shouldError500 := shouldTriggerError("error500", error500, currentIndex)
	shouldError400 := shouldTriggerError("error400", error400, currentIndex)
	shouldDisconnect := shouldTriggerError("disconnect", disconnect, currentIndex)
	shouldCorrupt := shouldTriggerError("corrupt", corrupt, currentIndex)
	trackerMutex.Unlock()

	if connectLatency > 0 {
		time.Sleep(time.Duration(connectLatency) * time.Second)
	}

	if shouldDisconnect {
		logger.Info("Disconnecting based on configured probability",
			zap.Int("request_num", errorTracker.RequestCounter),
			zap.Float64("disconnect", disconnect))

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

	if shouldNoBackend {
		logger.Info("Preventing backend request based on configured probability",
			zap.Int("request_num", errorTracker.RequestCounter),
			zap.Float64("no_backend", noBackend))

		time.Sleep(time.Duration(latency) * time.Second)
		c.JSON(http.StatusOK, gin.H{"message": "Response generated by Bad-Proxy without reaching backend"})
		return
	}

	if shouldError400 {
		logger.Info("Returning 400 Bad Request based on configured probability",
			zap.Int("request_num", errorTracker.RequestCounter),
			zap.Float64("error400", error400))

		time.Sleep(time.Duration(latency) * time.Second)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bad request error generated by Bad-Proxy"})
		return
	}

	if shouldError500 {
		logger.Info("Returning 500 Internal Server Error based on configured probability",
			zap.Int("request_num", errorTracker.RequestCounter),
			zap.Float64("error500", error500))

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

	if shouldCorrupt {
		logger.Info("Corrupting response based on configured probability",
			zap.Int("request_num", errorTracker.RequestCounter),
			zap.Float64("corrupt", corrupt))

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

func shouldTriggerError(errorType string, probability float64, currentIndex int) bool {
	if probability <= 0 {
		errorTracker.ErrorBuckets[errorType][currentIndex] = false
		return false
	}

	if probability >= 1 {
		errorTracker.ErrorBuckets[errorType][currentIndex] = true
		return true
	}

	errorBucket := errorTracker.ErrorBuckets[errorType]
	currentCount := countTrueValues(errorBucket)
	targetCount := int(float64(errorTracker.WindowSize) * probability)

	if currentCount < targetCount {
		errorBucket[currentIndex] = true
		return true
	} else {
		errorBucket[currentIndex] = false
		return false
	}
}

func countTrueValues(bucket []bool) int {
	count := 0
	for _, v := range bucket {
		if v {
			count++
		}
	}
	return count
}

func calculateCurrentErrorRates() map[string]float64 {
	rates := make(map[string]float64)
	windowSize := errorTracker.WindowSize
	if errorTracker.RequestCounter < windowSize {
		windowSize = errorTracker.RequestCounter
	}

	if windowSize == 0 {
		return map[string]float64{
			"500":        0,
			"400":        0,
			"disconnect": 0,
			"corrupt":    0,
			"no_backend": 0,
		}
	}

	for errorType, bucket := range errorTracker.ErrorBuckets {
		trueCount := 0
		for i := 0; i < windowSize; i++ {
			if bucket[i] {
				trueCount++
			}
		}
		rates[errorType] = float64(trueCount) / float64(windowSize)
	}

	return map[string]float64{
		"500":        rates["error500"],
		"400":        rates["error400"],
		"disconnect": rates["disconnect"],
		"corrupt":    rates["corrupt"],
		"no_backend": rates["no_backend"],
	}
}

func resetErrorTrackers() {
	errorTracker.RequestCounter = 0
	for errorType := range errorTracker.ErrorBuckets {
		errorTracker.ErrorBuckets[errorType] = make([]bool, errorTracker.WindowSize)
	}
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}

	return value
}
