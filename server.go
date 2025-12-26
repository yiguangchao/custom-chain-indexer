package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

// StartServer Start the web server
// Receive a db pointer for querying libraries in the Controller
func StartServer(db *gorm.DB) {
	// 1. Initialize Gin engine
	r := gin.Default()

	// ==========================================
	// Prometheus monitoring interface
	// ==========================================
	// Prometheus will access this interface every few seconds and take away the data
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// 2. Define routing
	// GET /api/v1/transfers?address=0x123...&limit=10
	r.GET("/api/v1/transfers", func(c *gin.Context) {
		address := c.Query("address")
		limit := 10 // Default check for 10 items

		if address == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Address parameter must be provided"})
			return
		}

		var logs []TransferLog

		// 3. Query the database
		// SELECT * FROM transfer_logs WHERE from_address = ? OR to_address = ? ORDER BY block_number DESC LIMIT 10
		result := db.Where("from_address = ? OR to_address = ?", address, address).
			Order("block_number desc").
			Limit(limit).
			Find(&logs)

		if result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}

		// 4. Return JSON
		c.JSON(http.StatusOK, gin.H{
			"data":  logs,
			"total": len(logs),
		})
	})

	// 3. Start listening (default port 8080)
	// This step will block, so it needs to run in the main coroutine
	log.Println(">>> API The server starts on port 8080...")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Server startup failed:", err)
	}
}
