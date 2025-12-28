package main

import (
	"log"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

// 1. Define Request Parameter Structure (DTO)
// Use Gin's tag to automatically bind parameters and set default values
type TransferRequest struct {
	Address  string `form:"address" binding:"required"`
	Page     int    `form:"page,default=1" binding:"min=1"`
	PageSize int    `form:"page_size,default=10" binding:"min=1,max=100"`
}

// 2. Define a unified response structure
// Format that makes the front-end comfortable: including data and metadata
type PaginatedResponse struct {
	Data       interface{} `json:"data"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
	TotalPages int         `json:"total_pages"`
}

func StartServer(db *gorm.DB) {
	r := gin.Default()

	// monitoring interface
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Core query interface
	r.GET("/api/v1/transfers", func(c *gin.Context) {
		var req TransferRequest

		// A. Parameter binding and verification
		// If the parameter is incorrect (such as paginate=1000), Gin will directly report an error and return 400
		if err := c.ShouldBindQuery(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		// B. Prepare the basic builder for querying (Builder)
		// Note: Do not execute the query here, only spell out the WHERE condition
		query := db.Model(&TransferLog{}).
			Where("from_address = ? OR to_address = ?", req.Address, req.Address)

		// C. Query the total number of articles (used to calculate the total number of pages)
		// This step must be done before Limit/Offset
		var total int64
		if err := query.Count(&total).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to query the total number"})
			return
		}

		// D. Execute pagination query
		// Formula: Offset=(page -1) * page size
		offset := (req.Page - 1) * req.PageSize

		var logs []TransferLog
		result := query.Order("block_number desc").
			Limit(req.PageSize).
			Offset(offset).
			Find(&logs)

		if result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}

		// E. Calculate the total number of pages
		totalPages := int(math.Ceil(float64(total) / float64(req.PageSize)))

		// F. Return standard JSON
		c.JSON(http.StatusOK, PaginatedResponse{
			Data:       logs,
			Total:      total,
			Page:       req.Page,
			PageSize:   req.PageSize,
			TotalPages: totalPages,
		})
	})

	log.Println(">>> The API server starts on port 8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Server startup failed:", err)
	}
}
