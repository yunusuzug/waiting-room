package waitingroom

import (
	"sync"
)

// handlerRegistry maintains a mapping of task types to their handlers.
// It is safe for concurrent use.
type handlerRegistry struct {
	handlers map[string]TaskHandler
	mu       sync.RWMutex
}

// newHandlerRegistry creates a new empty registry.
func newHandlerRegistry() *handlerRegistry {
	return &handlerRegistry{
		handlers: make(map[string]TaskHandler),
	}
}

// register adds a task handler to the registry.
// Returns ErrHandlerAlreadyRegistered if a handler for this type already exists.
func (r *handlerRegistry) register(handler TaskHandler) error {
	if handler == nil {
		return ErrHandlerNotImplemented
	}

	taskType := handler.Type()
	if taskType == "" {
		return ErrInvalidPayload
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[taskType]; exists {
		return ErrHandlerAlreadyRegistered
	}

	r.handlers[taskType] = handler
	return nil
}

// unregister removes a task handler from the registry.
func (r *handlerRegistry) unregister(taskType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handlers, taskType)
}

// get retrieves a handler by task type.
// Returns ErrHandlerNotFound if the handler is not registered.
func (r *handlerRegistry) get(taskType string) (TaskHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, exists := r.handlers[taskType]
	if !exists {
		return nil, ErrHandlerNotFound
	}

	return handler, nil
}

// has checks if a handler is registered for the given task type.
func (r *handlerRegistry) has(taskType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.handlers[taskType]
	return exists
}

// types returns a list of all registered task types.
func (r *handlerRegistry) types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		types = append(types, t)
	}
	return types
}

// count returns the number of registered handlers.
func (r *handlerRegistry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handlers)
}
