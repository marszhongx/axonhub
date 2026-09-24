package pelican

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/looplj/axonhub/internal/contexts"
)

// Handlers exposes the module over the admin HTTP API.
type Handlers struct {
	service *Service
}

// NewHandlers wires the handlers.
func NewHandlers(service *Service) *Handlers { return &Handlers{service: service} }

// RegisterRoutes mounts the module endpoints. This is the only line AxonHub's router needs.
func (h *Handlers) RegisterRoutes(group *gin.RouterGroup) {
	pelican := group.Group("/pelican")
	pelican.GET("/config", h.getConfig)
	pelican.PUT("/config", h.saveConfig)
	pelican.POST("/rounds", h.startRound)
	pelican.GET("/results", h.listResults)
	pelican.GET("/results/:id/artifact", h.getArtifact)
	pelican.GET("/results/:id/conversation", h.getConversation)
}

type configPayload struct {
	Prompt  string   `json:"prompt"`
	Targets []Target `json:"targets"`
	Enabled bool     `json:"scheduleEnabled"`
}

func (h *Handlers) getConfig(c *gin.Context) {
	config, err := h.service.Config()
	if err != nil {
		serverError(c, err)
		return
	}
	status, err := h.service.Status()
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"prompt":          config.Prompt,
		"targets":         config.Targets,
		"scheduleEnabled": config.Schedule.Enabled,
		"nextRunAt":       status.NextRunAt,
		"running":         status.Running,
		"efforts":         status.Efforts,
		"defaultPrompt":   DefaultPrompt,
		"dataDir":         status.DataDir,
	})
}

func (h *Handlers) saveConfig(c *gin.Context) {
	var payload configPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	if err := validateTargets(payload.Targets); err != nil {
		badRequest(c, err.Error())
		return
	}

	projectID, _ := contexts.GetProjectID(c.Request.Context())
	if _, err := h.service.SaveConfig(projectID, Config{
		Prompt:   payload.Prompt,
		Targets:  payload.Targets,
		Schedule: Schedule{Enabled: payload.Enabled},
	}); err != nil {
		// Validation problems are the caller's fault, storage problems are ours.
		if isValidationError(err) {
			badRequest(c, err.Error())
			return
		}
		serverError(c, err)
		return
	}
	h.getConfig(c)
}

func (h *Handlers) startRound(c *gin.Context) {
	if err := h.service.RunNow(c.Request.Context()); err != nil {
		if isValidationError(err) {
			badRequest(c, err.Error())
			return
		}
		serverError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"started": true})
}

func (h *Handlers) listResults(c *gin.Context) {
	results, err := h.service.Results()
	if err != nil {
		serverError(c, err)
		return
	}
	status, err := h.service.Status()
	if err != nil {
		serverError(c, err)
		return
	}

	limit := 48
	if raw := c.Query("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	// The history can grow without bound; only the newest slice is returned.
	if len(results) > limit {
		results = results[:limit]
	}

	succeeded, failed := 0, 0
	for _, result := range results {
		switch result.Status {
		case StatusSucceeded:
			succeeded++
		case StatusFailed:
			failed++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"results":   results,
		"running":   status.Running,
		"nextRunAt": status.NextRunAt,
		"enabled":   status.Schedule.Enabled,
		"succeeded": succeeded,
		"failed":    failed,
		"total":     len(results),
	})
}

// sandboxPolicy isolates generated documents: no same-origin access, no network, no forms.
const sandboxPolicy = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; " +
	"style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; " +
	"frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

func (h *Handlers) getArtifact(c *gin.Context) {
	id := c.Param("id")
	result, err := h.service.FindResult(id)
	if err != nil {
		notFound(c, "result not found")
		return
	}
	if result.Status != StatusSucceeded || result.Format == "" {
		notFound(c, "this result has no document")
		return
	}
	content, err := h.service.Artifact(id, result.Format)
	if err != nil {
		notFound(c, "this result has no document")
		return
	}

	contentType := "text/html; charset=utf-8"
	if result.Format == "svg" {
		contentType = "image/svg+xml; charset=utf-8"
	}
	disposition := "inline"
	if c.Query("download") == "1" {
		disposition = "attachment"
	}

	c.Header("Content-Security-Policy", sandboxPolicy)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", disposition+"; filename="+id+"."+result.Format)
	c.Data(http.StatusOK, contentType, []byte(content))
}

func (h *Handlers) getConversation(c *gin.Context) {
	conversation, err := h.service.Conversation(c.Param("id"))
	if err != nil {
		notFound(c, "this result has no recorded conversation")
		return
	}
	c.JSON(http.StatusOK, conversation)
}

func badRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}

func notFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{"error": message})
}

func serverError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": RedactCredentials(err.Error())})
}

// isValidationError separates bad user input (400) from storage or gateway problems (500).
func isValidationError(err error) bool {
	message := err.Error()
	for _, fragment := range []string{
		"prompt must not be empty",
		"prompt must be at most",
		"model must not be empty",
		"unsupported reasoning effort",
		"select at least one model",
		"no model is selected",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
