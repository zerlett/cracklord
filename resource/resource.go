package resource

import (
	"code.google.com/p/go-uuid/uuid"
	"errors"
	"github.com/jmmcatee/cracklord/common"
	"log"
	"net"
	"net/rpc"
	"sync"
)

// TODO: Add function for adding tools and assign a UUID

const (
	ERROR_AUTH    = "Call to resource did not have the proper authentication token."
	ERROR_NO_TOOL = "Tool specified does not exit."
)

// This will need to be called with a WaitGroup to handle other calls without
// the program closing. A channel is provied to alert when the RPC server is done.
// This can be used to quit the application or simply restart the server for the next
// master to connect.
func StartResource(addr string, q *Queue) chan bool {
	res := rpc.NewServer()
	res.Register(q)

	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	quit := make(chan bool)
	go func() {
		// Accept and server a limited number of times
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}

		res.ServeConn(conn)

		l.Close()
		quit <- true
	}()

	return quit
}

type Queue struct {
	stack map[string]common.Tasker
	tools []common.Tooler
	sync.RWMutex
	authToken string
	hardware  map[string]bool
}

func NewResourceQueue(token string) Queue {
	return Queue{
		stack:     map[string]common.Tasker{},
		tools:     []common.Tooler{},
		authToken: token,
		hardware:  map[string]bool{},
	}
}

func (q *Queue) AddTool(tooler common.Tooler) {
	// Add the hardware used by the tool
	q.hardware[tooler.Requirements()] = true

	tooler.SetUUID(uuid.New())
	q.tools = append(q.tools, tooler)
}

// Task RPC functions

func (q *Queue) ResourceHardware(rpc common.RPCCall, hw *map[string]bool) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	q.RLock()
	defer q.RUnlock()

	*hw = q.hardware

	return nil
}

func (q *Queue) AddTask(rpc common.RPCCall, rj *common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// variable to hold the tasker
	var tasker common.Tasker
	var err error
	// loop through common.Toolers for matching tool
	q.RLock()
	for i, _ := range q.tools {
		if q.tools[i].UUID() == rpc.Job.ToolUUID {
			tasker, err = q.tools[i].NewTask(rpc.Job)
			if err != nil {
				return err
			}
		}
	}
	q.RUnlock()

	// Check if no tool was found and return error
	if tasker == nil {
		return errors.New(ERROR_NO_TOOL)
	}

	// Looks good so lets add to the stack
	q.Lock()
	if q.stack == nil {
		q.stack = make(map[string]common.Tasker)
	}

	q.stack[rpc.Job.UUID] = tasker

	// Everything should be paused by the control queue so start this job
	err = q.stack[rpc.Job.UUID].Run()
	if err != nil {
		return errors.New("Error starting task on the resource: " + err.Error())
	}

	// Grab the status and return that job to the control queue
	*rj = q.stack[rpc.Job.UUID].Status()
	q.Unlock()

	return nil
}

func (q *Queue) TaskStatus(rpc common.RPCCall, j *common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// Grab the task specified by the UUID and return its status
	q.Lock()
	_, ok := q.stack[rpc.Job.UUID]

	// Check for a bad UUID
	if ok != false {
		errors.New("Task with UUID provided does not exist.")
	}

	*j = q.stack[rpc.Job.UUID].Status()

	q.Unlock()

	return nil
}

func (q *Queue) TaskPause(rpc common.RPCCall, j *common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// Grab the task specified by the UUID
	q.Lock()
	_, ok := q.stack[rpc.Job.UUID]

	// Check for a bad UUID
	if ok {
		errors.New("Task with UUID provided does not exist.")
	}

	// Pause the task
	err := q.stack[rpc.Job.UUID].Pause()
	if err != nil {
		// return the error but quit the job with status Failed
		// This is a definied behavior that we will not for all tools
		q.stack[rpc.Job.UUID].Quit()
		return err
	}

	*j = q.stack[rpc.Job.UUID].Status()
	q.Unlock()

	return nil
}

func (q *Queue) TaskRun(rpc common.RPCCall, j *common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// Grab the task specified by the UUID
	q.Lock()
	_, ok := q.stack[rpc.Job.UUID]

	// Check for a bad UUID
	if ok != false {
		errors.New("Task with UUID provided does not exist.")
	}

	// Start or resume the task
	err := q.stack[rpc.Job.UUID].Run()
	if err != nil {
		return err
	}

	*j = q.stack[rpc.Job.UUID].Status()
	q.Unlock()

	return nil

}

func (q *Queue) TaskQuit(rpc common.RPCCall, j *common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// Grab the task specified by the UUID
	q.Lock()
	_, ok := q.stack[rpc.Job.UUID]

	// Check for a bad UUID
	if ok != false {
		errors.New("Task with UUID provided does not exist.")
	}

	// Quit the task and return the final result
	*j = q.stack[rpc.Job.UUID].Quit()

	// Remove quit job from stack
	delete(q.stack, rpc.Job.UUID)
	q.Unlock()

	return nil
}

// Queue Tasks

func (q *Queue) ResourceTools(rpc common.RPCCall, tools *[]common.Tool) error {
	q.RLock()
	defer q.RUnlock()

	var ts []common.Tool

	for i, _ := range q.tools {
		var tool common.Tool
		tool.Name = q.tools[i].Name()
		tool.Type = q.tools[i].Type()
		tool.Version = q.tools[i].Version()
		tool.UUID = q.tools[i].UUID()
		tool.Parameters = q.tools[i].Parameters()
		tool.Requirements = q.tools[i].Requirements()

		ts = append(ts, tool)
	}

	*tools = ts

	return nil
}

func (q *Queue) AllTaskStatus(rpc common.RPCCall, j *[]common.Job) error {
	// Check authentication token
	if rpc.Auth != q.authToken {
		return errors.New(ERROR_AUTH)
	}

	// Loop through any tasks in the stack and update their status while
	// grabing the Job object output
	var jobs []common.Job

	q.Lock()

	for i, _ := range q.stack {
		jobs = append(jobs, q.stack[i].Status())
	}

	*j = jobs

	q.Unlock()

	return nil
}