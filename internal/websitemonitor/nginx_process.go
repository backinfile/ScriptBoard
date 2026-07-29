package websitemonitor

import (
	"context"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

type NginxProcess struct {
	Name string
	Args []string
	CWD  string
}

type NginxProcessSource interface {
	Processes(context.Context) ([]NginxProcess, error)
}

type systemNginxProcessSource struct{}

func (systemNginxProcessSource) Processes(ctx context.Context) ([]NginxProcess, error) {
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	var result []NginxProcess
	for _, running := range processes {
		name, err := running.NameWithContext(ctx)
		if err != nil || !strings.EqualFold(name, "nginx") && !strings.EqualFold(name, "nginx.exe") {
			continue
		}
		args, _ := running.CmdlineSliceWithContext(ctx)
		cwd, _ := running.CwdWithContext(ctx)
		result = append(result, NginxProcess{Name: name, Args: args, CWD: cwd})
	}
	return result, nil
}
