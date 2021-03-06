// Copyright 2014 The rkt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//+build linux

package main

import (
	"encoding/json"
	"fmt"
	"time"

	lib "github.com/coreos/rkt/lib"
	pkgPod "github.com/coreos/rkt/pkg/pod"
	"github.com/spf13/cobra"
)

var (
	cmdStatus = &cobra.Command{
		Use:   "status [--wait] UUID",
		Short: "Check the status of a rkt pod",
		Long: `Prints assorted information about the pod such as its state, pid and exit
status`,
		Run: runWrapper(runStatus),
	}
	flagWait bool
)

const (
	overlayStatusDirTemplate = "overlay/%s/upper/rkt/status"
	regularStatusDir         = "stage1/rootfs/rkt/status"
	cmdStatusName            = "status"
)

func init() {
	cmdRkt.AddCommand(cmdStatus)
	cmdStatus.Flags().BoolVar(&flagWait, "wait", false, "toggle waiting for the pod to exit")
	cmdStatus.Flags().Var(&flagFormat, "format", "choose the output format, allowed format includes 'json', 'json-pretty'. If empty, then the result is printed as key value pairs")
}

func runStatus(cmd *cobra.Command, args []string) (exit int) {
	if len(args) != 1 {
		cmd.Usage()
		return 254
	}

	p, err := pkgPod.PodFromUUIDString(getDataDir(), args[0])
	if err != nil {
		stderr.PrintE("problem retrieving pod", err)
		return 254
	}
	defer p.Close()

	if flagWait {
		if err := p.WaitExited(); err != nil {
			stderr.PrintE("unable to wait for pod", err)
			return 254
		}
	}

	if err = printStatus(p); err != nil {
		stderr.PrintE("unable to print status", err)
		return 254
	}

	return 0
}

// getExitStatuses returns a map of the statuses of the pod.
func getExitStatuses(p *pkgPod.Pod) (map[string]int, error) {
	_, manifest, err := p.PodManifest()
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int)
	for _, app := range manifest.Apps {
		exitCode, err := p.AppExitCode(app.Name.String())
		if err != nil {
			continue
		}
		stats[app.Name.String()] = exitCode
	}
	return stats, nil
}

// printStatus prints the pod's pid and per-app status codes
func printStatus(p *pkgPod.Pod) error {
	if flagFormat != outputFormatTabbed {
		pod, err := lib.NewPodFromInternalPod(p)
		if err != nil {
			return fmt.Errorf("error converting pod: %v", err)
		}
		switch flagFormat {
		case outputFormatJSON:
			result, err := json.Marshal(pod)
			if err != nil {
				return fmt.Errorf("error marshaling the pod: %v", err)
			}
			stdout.Print(string(result))
		case outputFormatPrettyJSON:
			result, err := json.MarshalIndent(pod, "", "\t")
			if err != nil {
				return fmt.Errorf("error marshaling the pod: %v", err)
			}
			stdout.Print(string(result))
		}
		return nil
	}

	state := p.State()
	stdout.Printf("state=%s", state)

	created, err := p.CreationTime()
	if err != nil {
		return fmt.Errorf("unable to get creation time for pod %q: %v", p.UUID, err)
	}
	createdStr := created.Format(defaultTimeLayout)

	stdout.Printf("created=%s", createdStr)

	started, err := p.StartTime()
	if err != nil {
		return fmt.Errorf("unable to get start time for pod %q: %v", p.UUID, err)
	}
	var startedStr string
	if !started.IsZero() {
		startedStr = started.Format(defaultTimeLayout)
		stdout.Printf("started=%s", startedStr)
	}

	if state == pkgPod.Running {
		stdout.Printf("networks=%s", fmtNets(p.Nets))
	}

	if state == pkgPod.Running || state == pkgPod.Deleting || state == pkgPod.ExitedDeleting || state == pkgPod.Exited || state == pkgPod.ExitedGarbage {
		var pid int
		pidCh := make(chan int, 1)

		// Wait slightly because the pid file might not be written yet when the state changes to 'Running'.
		go func() {
			for {
				pid, err := p.Pid()
				if err == nil {
					pidCh <- pid
					return
				}
				time.Sleep(time.Millisecond * 100)
			}
		}()

		select {
		case pid = <-pidCh:
		case <-time.After(time.Second):
			return fmt.Errorf("unable to get PID for pod %q: %v", p.UUID, err)
		}

		stdout.Printf("pid=%d\nexited=%t", pid, (state == pkgPod.Exited || state == pkgPod.ExitedGarbage))

		if state != pkgPod.Running {
			stats, err := getExitStatuses(p)
			if err != nil {
				return fmt.Errorf("unable to get exit statuses for pod %q: %v", p.UUID, err)
			}
			for app, stat := range stats {
				stdout.Printf("app-%s=%d", app, stat)
			}
		}
	}
	return nil
}
