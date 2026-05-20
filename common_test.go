package gstate

type StateID string
type EventID string
type Context struct {
	Count int
}

func (c Context) Clone() Context {
	return c
}

