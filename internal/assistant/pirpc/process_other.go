//go:build !windows && !linux

package pirpc

import (
	"os"
	"os/exec"
)

type portableAssistantProcessController struct{ process *os.Process }

func configureAssistantProcess(_ *exec.Cmd) {}

func attachAssistantProcess(process *os.Process) (assistantProcessController, error) {
	return &portableAssistantProcessController{process: process}, nil
}

func (controller *portableAssistantProcessController) Terminate(force bool) error {
	if force {
		return controller.process.Kill()
	}
	return controller.process.Signal(os.Interrupt)
}

func (controller *portableAssistantProcessController) Close() error { return nil }

func mkdirPrivate(path string) error { return os.MkdirAll(path, 0o700) }
