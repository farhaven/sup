package sup

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"text/template"

	"github.com/alessio/shellescape"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// Task represents a set of commands to be run.
type CommandTask struct {
	run     string
	input   io.Reader
	clients []Client
	tty     bool
}

func (t *CommandTask) Run() string {
	return t.run
}

func (t *CommandTask) Input() io.Reader {
	return t.input
}

func (t *CommandTask) Clients() []Client {
	return t.clients
}

func (t *CommandTask) TTY() bool {
	return t.tty
}

type TemplateTask struct {
	tmpl *template.Template
	dst string
	vars map[string]interface{}
	clients []Client
}

func (t *TemplateTask) Run() string {
	return "cat > " + shellescape.Quote(t.dst)
}

func (t *TemplateTask) InputForClient(c Client) (io.Reader, error) {
	var buffer bytes.Buffer
	context := struct{
		Client Client
		Vars map[string]interface{}
	}{c, t.vars}
	if err := t.tmpl.Execute(&buffer, context); err != nil {
		return nil, err
	}
	return &buffer, nil
}

func (t *TemplateTask) Clients() []Client {
	return t.clients
}

func (t *TemplateTask) TTY() bool {
	return false
}

type Task interface {
	Run() string
	Clients() []Client
	TTY() bool
}

func (sup *Stackup) createTasks(cmd *Command, clients []Client, env string) ([]Task, error) {
	var tasks []Task

	cwd, err := os.Getwd()
	if err != nil {
		return nil, errors.Wrap(err, "resolving CWD failed")
	}

	// Anything to upload?
	for _, upload := range cmd.Upload {
		uploadFile, err := ResolveLocalPath(cwd, upload.Src, env)
		if err != nil {
			return nil, errors.Wrap(err, "upload: "+upload.Src)
		}
		uploadTarReader, err := NewTarStreamReader(cwd, uploadFile, upload.Exc)
		if err != nil {
			return nil, errors.Wrap(err, "upload: "+upload.Src)
		}

		task := CommandTask{
			run:   RemoteTarCommand(upload.Dst),
			input: uploadTarReader,
			tty:   false,
		}

		if cmd.Once {
			task.clients = []Client{clients[0]}
			tasks = append(tasks, &task)
		} else if cmd.Serial > 0 {
			// Each "serial" task client group is executed sequentially.
			for i := 0; i < len(clients); i += cmd.Serial {
				j := i + cmd.Serial
				if j > len(clients) {
					j = len(clients)
				}
				copy := task
				copy.clients = clients[i:j]
				tasks = append(tasks, &copy)
			}
		} else {
			task.clients = clients
			tasks = append(tasks, &task)
		}
	}

	if cmd.Template.Src != "" && cmd.Template.Dst != "" {
		f, err := os.Open(cmd.Template.Src)
		if err != nil {
			return nil, errors.Wrap(err, "can't open template")
		}

		data, err := ioutil.ReadAll(f)
		if err != nil {
			return nil, errors.Wrap(err, "can't open template")
		}

		tmpl, err := template.New("tpl").Parse(string(data))
		if err != nil {
			return nil, errors.Wrap(err, "can't parse template")
		}

		vars := make(map[string]interface{})
		if cmd.Template.Vars != "" {
			varf, err := os.Open(cmd.Template.Vars)
			if err != nil {
				return nil, errors.Wrap(err, "can't read variables")
			}
			vardata, err := ioutil.ReadAll(varf)
			if err != nil {
				return nil, errors.Wrap(err, "can't read variables")
			}

			if err = yaml.Unmarshal(vardata, &vars); err != nil {
				return nil, errors.Wrap(err, "can't parse variables")
			}
		}

		task := TemplateTask{
			tmpl: tmpl,
			dst: cmd.Template.Dst,
			vars: vars,
		}

		if cmd.Serial > 0 {
			// Each "serial" task client group is executed sequentially.
			for i := 0; i < len(clients); i += cmd.Serial {
				j := i + cmd.Serial
				if j > len(clients) {
					j = len(clients)
				}
				copy := task
				copy.clients = clients[i:j]
				tasks = append(tasks, &copy)
			}
		} else {
			task.clients = clients
			tasks = append(tasks, &task)
		}
	}

	// Script. Read the file as a multiline input command.
	if cmd.Script != "" {
		f, err := os.Open(cmd.Script)
		if err != nil {
			return nil, errors.Wrap(err, "can't open script")
		}
		data, err := ioutil.ReadAll(f)
		if err != nil {
			return nil, errors.Wrap(err, "can't read script")
		}

		task := CommandTask{
			run: string(data),
			tty: true,
		}
		if sup.debug {
			task.run = "set -x;" + task.run
		}
		if cmd.Stdin {
			task.input = os.Stdin
		}
		if cmd.Once {
			task.clients = []Client{clients[0]}
			tasks = append(tasks, &task)
		} else if cmd.Serial > 0 {
			// Each "serial" task client group is executed sequentially.
			for i := 0; i < len(clients); i += cmd.Serial {
				j := i + cmd.Serial
				if j > len(clients) {
					j = len(clients)
				}
				copy := task
				copy.clients = clients[i:j]
				tasks = append(tasks, &copy)
			}
		} else {
			task.clients = clients
			tasks = append(tasks, &task)
		}
	}

	// Local command.
	if cmd.Local != "" {
		local := &LocalhostClient{
			env: env + `export SUP_HOST="localhost";`,
		}
		local.Connect("localhost")
		task := &CommandTask{
			run:     cmd.Local,
			clients: []Client{local},
			tty:     true,
		}
		if sup.debug {
			task.run = "set -x;" + task.run
		}
		if cmd.Stdin {
			task.input = os.Stdin
		}
		tasks = append(tasks, task)
	}

	// Remote command.
	if cmd.Run != "" {
		task := CommandTask{
			run: cmd.Run,
			tty: true,
		}
		if sup.debug {
			task.run = "set -x;" + task.run
		}
		if cmd.Stdin {
			task.input = os.Stdin
		}
		if cmd.Once {
			task.clients = []Client{clients[0]}
			tasks = append(tasks, &task)
		} else if cmd.Serial > 0 {
			// Each "serial" task client group is executed sequentially.
			for i := 0; i < len(clients); i += cmd.Serial {
				j := i + cmd.Serial
				if j > len(clients) {
					j = len(clients)
				}
				copy := task
				copy.clients = clients[i:j]
				tasks = append(tasks, &copy)
			}
		} else {
			task.clients = clients
			tasks = append(tasks, &task)
		}
	}

	return tasks, nil
}

type ErrTask struct {
	Task   Task
	Reason string
}

func (e ErrTask) Error() string {
	return fmt.Sprintf(`Run("%v"): %v`, e.Task, e.Reason)
}
