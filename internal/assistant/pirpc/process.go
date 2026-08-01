package pirpc

type assistantProcessController interface {
	Terminate(force bool) error
	Close() error
}
