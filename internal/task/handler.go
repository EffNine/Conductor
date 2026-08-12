package task

import (
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CreateTaskRequest is the body for POST /api/tasks.
type CreateTaskRequest struct {
	Input    string `json:"input"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	MaxSteps *int   `json:"max_steps,omitempty"`
}

// Handler holds HTTP handlers for the task API.
type Handler struct {
	store    Store
	executor Executor
	logger   *zap.Logger
}

// NewHandler creates a new task HTTP handler.
func NewHandler(store Store, exec Executor, logger *zap.Logger) *Handler {
	return &Handler{store: store, executor: exec, logger: logger}
}

// Register registers task API routes on the Fiber app.
func (h *Handler) Register(app *fiber.App) {
	group := app.Group("/api")
	group.Post("/tasks", h.HandleCreate)
	group.Get("/tasks", h.HandleList)
	group.Get("/tasks/:id", h.HandleGet)
	group.Post("/tasks/:id/cancel", h.HandleCancel)
	group.Post("/tasks/:id/retry", h.HandleRetry)
	group.Post("/tasks/:id/resume", h.HandleResume)
}

// HandleCreate creates and executes a new task.
func (h *Handler) HandleCreate(c *fiber.Ctx) error {
	var req CreateTaskRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Input == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "input is required",
		})
	}

	task := &Task{
		ID:       uuid.New().String(),
		Status:   StatusPending,
		Input:    req.Input,
		Priority: 0,
	}
	if req.Provider != "" {
		task.Provider = req.Provider
	}
	if req.Model != "" {
		task.Model = req.Model
	}
	if req.MaxSteps != nil && *req.MaxSteps > 0 {
		task.MaxSteps = *req.MaxSteps
	}

	if err := h.store.CreateTask(task); err != nil {
		h.logger.Error("failed to create task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create task",
		})
	}

	if err := h.executor.Execute(c.Context(), task.ID); err != nil {
		h.logger.Error("task execution failed", zap.String("task_id", task.ID), zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "task execution failed: " + err.Error(),
		})
	}

	updated, err := h.store.GetTask(task.ID)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to retrieve created task",
		})
	}
	return c.JSON(taskResponse(updated))
}

// HandleRetry re-runs a failed task from its latest checkpoint (if any).
func (h *Handler) HandleRetry(c *fiber.Ctx) error {
	id := c.Params("id")
	task, err := h.store.GetTask(id)
	if err == ErrTaskNotFound {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"error": "task not found",
		})
	}
	if err != nil {
		h.logger.Error("failed to get task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get task",
		})
	}
	if task.Status != StatusFailed {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "can only retry failed tasks (current: " + string(task.Status) + ")",
		})
	}
	if task.MaxRetries == 0 {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "retries not allowed (max_retries=0)",
		})
	}
	if task.RetryCount >= task.MaxRetries {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "max retries exceeded",
		})
	}
	if _, incErr := h.store.IncrementRetry(id); incErr != nil {
		h.logger.Error("failed to increment retry count", zap.Error(incErr))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to retry task",
		})
	}
	if err := h.executor.Execute(c.Context(), id); err != nil {
		h.logger.Error("retry execution failed", zap.String("task_id", id), zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "retry failed: " + err.Error(),
		})
	}
	updated, _ := h.store.GetTask(id)
	if updated == nil {
		return c.JSON(fiber.Map{"id": id, "status": string(StatusCompleted)})
	}
	return c.JSON(taskResponse(updated))
}

// HandleResume resumes a paused or interrupted task from its checkpoint.
func (h *Handler) HandleResume(c *fiber.Ctx) error {
	id := c.Params("id")
	task, err := h.store.GetTask(id)
	if err == ErrTaskNotFound {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"error": "task not found",
		})
	}
	if err != nil {
		h.logger.Error("failed to get task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get task",
		})
	}
	if task.Status != StatusPaused && task.Status != StatusRunning {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "can only resume paused or running tasks (current: " + string(task.Status) + ")",
		})
	}
	if len(task.Checkpoint) == 0 {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "no checkpoint found for resume",
		})
	}
	if err := h.executor.Execute(c.Context(), id); err != nil {
		h.logger.Error("resume execution failed", zap.String("task_id", id), zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "resume failed: " + err.Error(),
		})
	}
	updated, _ := h.store.GetTask(id)
	if updated == nil {
		return c.JSON(fiber.Map{"id": id, "status": string(StatusCompleted)})
	}
	return c.JSON(taskResponse(updated))
}

// HandleList returns a paginated list of tasks.
func (h *Handler) HandleList(c *fiber.Ctx) error {
	limitStr := c.Query("limit", "20")
	offsetStr := c.Query("offset", "0")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	tasks, err := h.store.ListTasks(limit, offset)
	if err != nil {
		h.logger.Error("failed to list tasks", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list tasks",
		})
	}
	return c.JSON(fiber.Map{
		"tasks":  tasks,
		"limit":  limit,
		"offset": offset,
	})
}

// HandleGet returns a single task by ID.
func (h *Handler) HandleGet(c *fiber.Ctx) error {
	id := c.Params("id")
	task, err := h.store.GetTask(id)
	if err == ErrTaskNotFound {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"error": "task not found",
		})
	}
	if err != nil {
		h.logger.Error("failed to get task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get task",
		})
	}
	return c.JSON(taskResponse(task))
}

// HandleCancel cancels a non-terminal task.
func (h *Handler) HandleCancel(c *fiber.Ctx) error {
	id := c.Params("id")
	task, err := h.store.GetTask(id)
	if err == ErrTaskNotFound {
		return c.Status(http.StatusNotFound).JSON(fiber.Map{
			"error": "task not found",
		})
	}
	if err != nil {
		h.logger.Error("failed to get task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get task",
		})
	}
	if task.Status.IsTerminal() {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"error": "cannot cancel terminal task (" + string(task.Status) + ")",
		})
	}
	if err := h.store.UpdateStatus(id, StatusCancelled); err != nil {
		h.logger.Error("failed to cancel task", zap.Error(err))
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to cancel task",
		})
	}
	updated, _ := h.store.GetTask(id)
	if updated == nil {
		return c.JSON(fiber.Map{"id": id, "status": string(StatusCancelled)})
	}
	return c.JSON(taskResponse(updated))
}

func taskResponse(t *Task) fiber.Map {
	return fiber.Map{
		"id":           t.ID,
		"status":       string(t.Status),
		"input":        t.Input,
		"output":       t.Output,
		"provider":     t.Provider,
		"model":        t.Model,
		"step_count":   t.StepCount,
		"retry_count":  t.RetryCount,
		"max_retries":  t.MaxRetries,
		"error":        t.Error,
		"created_at":   t.CreatedAt,
		"started_at":   t.StartedAt,
		"completed_at": t.CompletedAt,
	}
}

// compile-time check: SQLiteStore satisfies Store.
var _ Store = (*SQLiteStore)(nil)
