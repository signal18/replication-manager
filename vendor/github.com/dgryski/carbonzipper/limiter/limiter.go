package limiter

// ServerLimiter provides interface to limit amount of requests
type ServerLimiter map[string]chan struct{}

// NewServerLimiter creates a limiter for specific servers list.
func NewServerLimiter(servers []string, l int) ServerLimiter {
	sl := make(map[string]chan struct{})

	for _, s := range servers {
		sl[s] = make(chan struct{}, l)
	}

	return sl
}

// Enter claims one of free slots or blocks until there is one.
func (sl ServerLimiter) Enter(s string) {
	if sl == nil {
		return
	}
	sl[s] <- struct{}{}
}

// Frees a slot in limiter
func (sl ServerLimiter) Leave(s string) {
	if sl == nil {
		return
	}
	<-sl[s]
}
