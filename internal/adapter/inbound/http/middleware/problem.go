package middleware

import (
	"github.com/gin-gonic/gin"
)

const problemContentType = "application/problem+json"

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	Code      string `json:"code"`
	TraceID   string `json:"trace_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func abortProblem(c *gin.Context, status int, typeURI, title, code, detail string) {
	c.Header("Content-Type", problemContentType)
	c.AbortWithStatusJSON(status, problem{
		Type:      typeURI,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  c.Request.URL.Path,
		Code:      code,
		TraceID:   c.Writer.Header().Get("X-Trace-ID"),
		RequestID: c.Writer.Header().Get("X-Request-ID"),
	})
}
