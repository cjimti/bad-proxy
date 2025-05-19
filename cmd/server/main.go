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
	Latency    int     `json:"latency"`
	Error500   float64 `json:"500"`
	Error400   float64 `json:"400"`
	Disconnect float64 `json:"disconnect"`
	Corrupt    float64 `json:"corrupt"`
}

var (
	config = ProxyConfig{
		Latency:    0,
		Error500:   0,
		Error400:   0,
		Disconnect: 0,
		Corrupt:    0,
	}
	configMutex sync.RWMutex
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
		c.JSON(http.StatusOK, currentConfig)
	})

	rCfg.POST("/config", func(c *gin.Context) {
		var newConfig ProxyConfig
		if err := c.ShouldBindJSON(&newConfig); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid configuration format"})
			return
		}

		configMutex.Lock()
		config = newConfig
		configMutex.Unlock()

		logger.Info("Proxy configuration updated",
			zap.Int("latency", newConfig.Latency),
			zap.Float64("500", newConfig.Error500),
			zap.Float64("400", newConfig.Error400),
			zap.Float64("disconnect", newConfig.Disconnect),
			zap.Float64("corrupt", newConfig.Corrupt),
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
	error500 := config.Error500
	error400 := config.Error400
	disconnect := config.Disconnect
	corrupt := config.Corrupt
	configMutex.RUnlock()

	if latency > 0 {
		time.Sleep(time.Duration(latency) * time.Second)
	}

	random := rand.Float64()

	if random < disconnect {
		logger.Info("Disconnecting based on configured probability",
			zap.Float64("random", random),
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

	if random < error400 {
		logger.Info("Returning 400 Bad Request based on configured probability",
			zap.Float64("random", random),
			zap.Float64("error400", error400))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bad request error generated by Bad-Proxy"})
		return
	}

	if random < error500 {
		logger.Info("Returning 500 Internal Server Error based on configured probability",
			zap.Float64("random", random),
			zap.Float64("error500", error500))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Server error generated by Bad-Proxy"})
		return
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

	random = rand.Float64()
	if random < corrupt {
		logger.Info("Corrupting response based on configured probability",
			zap.Float64("random", random),
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

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return fallback
	}

	return value
}
