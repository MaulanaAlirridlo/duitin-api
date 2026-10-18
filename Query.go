package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

type pageOptions struct {
	Page   int64 `json:"page"`
	Limit  int64 `json:"limit"`
	Offset int64 `json:"-"`
}

type pageMetadata struct {
	Page       int64 `json:"page"`
	Limit      int64 `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

type sqlFilters struct {
	clauses []string
	args    []any
}

type dateRange struct {
	Start string
	End   string
}

func parsePageOptions(c *gin.Context) (pageOptions, bool) {
	page := pageOptions{Page: 1, Limit: defaultPageSize}
	var err error

	if raw := c.Query("page"); raw != "" {
		page.Page, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || page.Page < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
			return pageOptions{}, false
		}
	}
	if raw := c.Query("limit"); raw != "" {
		page.Limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || page.Limit < 1 || page.Limit > maxPageSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 100"})
			return pageOptions{}, false
		}
	}
	if page.Page-1 > (int64(^uint64(0)>>1))/page.Limit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page is too large"})
		return pageOptions{}, false
	}
	page.Offset = (page.Page - 1) * page.Limit
	return page, true
}

func (f *sqlFilters) add(predicate string, value any) {
	placeholder := fmt.Sprintf("$%d", len(f.args)+1)
	f.clauses = append(f.clauses, strings.Replace(predicate, "?", placeholder, 1))
	f.args = append(f.args, value)
}

func (f sqlFilters) where() string {
	if len(f.clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.clauses, " AND ")
}

func (f sqlFilters) argsWith(values ...any) []any {
	args := append([]any(nil), f.args...)
	return append(args, values...)
}

func (f *sqlFilters) addSearch(column, search string) {
	search = strings.TrimSpace(search)
	if search != "" {
		f.add("POSITION(LOWER(?) IN LOWER("+column+")) > 0", search)
	}
}

func parseDateRange(c *gin.Context, defaults bool) (dateRange, bool) {
	rangeFilter := dateRange{
		Start: strings.TrimSpace(c.Query("start_date")),
		End:   strings.TrimSpace(c.Query("end_date")),
	}
	now := time.Now()
	if defaults {
		if rangeFilter.Start == "" {
			rangeFilter.Start = now.AddDate(0, 0, -29).Format("2006-01-02")
		}
		if rangeFilter.End == "" {
			rangeFilter.End = now.Format("2006-01-02")
		}
	}

	if rangeFilter.Start != "" {
		start, err := time.Parse("2006-01-02", rangeFilter.Start)
		if err != nil || start.Year() < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start_date must use YYYY-MM-DD format"})
			return dateRange{}, false
		}
	}
	if rangeFilter.End != "" {
		end, err := time.Parse("2006-01-02", rangeFilter.End)
		if err != nil || end.Year() < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end_date must use YYYY-MM-DD format"})
			return dateRange{}, false
		}
	}
	if rangeFilter.Start != "" && rangeFilter.End != "" && rangeFilter.Start > rangeFilter.End {
		c.JSON(http.StatusBadRequest, gin.H{"error": "start_date must not be after end_date"})
		return dateRange{}, false
	}
	return rangeFilter, true
}

func addDateRange(filters *sqlFilters, dateColumn string, dates dateRange) {
	if dates.Start != "" {
		filters.add(dateColumn+" >= ?::date", dates.Start)
	}
	if dates.End != "" {
		filters.add(dateColumn+" <= ?::date", dates.End)
	}
}

func respondPaginated(c *gin.Context, data any, total int64, page pageOptions) {
	totalPages := total / int64(page.Limit)
	if total%int64(page.Limit) > 0 {
		totalPages++
	}
	c.JSON(http.StatusOK, gin.H{
		"data": data,
		"pagination": pageMetadata{
			Page:       page.Page,
			Limit:      page.Limit,
			Total:      total,
			TotalPages: totalPages,
		},
	})
}
