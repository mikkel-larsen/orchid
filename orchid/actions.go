/*
Implementation of the Api for executing commands locally
*/

package main

import (
	"errors"
	"fmt"
	"github.com/hpcloud/tail"
	"os"
	"os/exec"
	"strings"
)

type Actions struct {
	path string
}

/*
List all jobs
*/
func (a *Actions) ListJobs() {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	for _, job := range setup.Jobs {
		fmt.Println(job.Id)
		for _, ex := range job.Pipeline {
			fmt.Printf("\t%s -> %s %v\n", ex.Machine, ex.Script, ex.Args)
		}
	}
}

/*
List all actions
*/
func (a *Actions) ListActions() {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	for _, action := range setup.Actions {
		fmt.Println(action.Id)
		fmt.Printf("\t%s -> %s\n",
			action.Machine,
			action.Command,
		)
	}
}

/*
List all machines
*/
func (a *Actions) ListMachines() {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	for _, machine := range setup.Machines {
		fmt.Println(machine.Id)
		fmt.Printf("\t%s@%s:%s (%s)\n", machine.User, machine.Address, machine.Port, machine.PrivateKey)
	}
}

/*
List all scripts
*/
func (a *Actions) ListScripts() {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	for _, script := range setup.Scripts {
		fmt.Println(script)
	}
}

/*
List all existing logs stored locally
*/
func (a *Actions) ListLogs() {
	logs, err := loadLogs(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	fmt.Printf("%-20s\t%-20s\t%-20s\t%-32s\t%-32s\n", "Id", "Job", "Status", "Start", "End")
	for _, log := range logs {
		fmt.Printf("%-20s\t%-20s\t%-20s\t%-32s\t%-32s\n", log.Id, log.JobId, log.Status, log.StartTime, log.EndTime)
	}
}

/*
Run the job with the given id
*/
func (a *Actions) RunJob(jobId string) {
	log := newLog(jobId)

	pipeline, err := buildPipeline(a.path, jobId, log)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	go func() {
		pipeline.Run(a.path)
	}()

	fmt.Println(log.Id)

	// Tail the log, ensuring the program does not terminate
	a.GetLogOutput(log.Id)
}

/*
Execute the action with the given id
*/
func (a *Actions) ExecuteAction(actionId string) error {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	// Find the action
	var action Action
	found := false
	for _, a := range setup.Actions {
		if a.Id == actionId {
			action = a
			found = true
			break
		}
	}

	// Check if no action matched
	if !found {
		return errors.New("No action with the given id was found")
	}

	var cmd *exec.Cmd

	if action.Machine == "local" {
		// If the script is to be executed locally, do so
		cmd = exec.Command(action.Command)
	} else {
		// If not to be executed locally, find the machine
		var machine Machine
		found = false
		for _, m := range setup.Machines {
			if m.Id == action.Machine {
				machine = m
				found = true
				break
			}
		}

		// Check if no machine matched
		if !found {
			return errors.New("No machine with the given id was found")
		}

		// Do the execution
		sshCommand := fmt.Sprintf(
			"ssh -tt -o 'StrictHostKeyChecking no' -o 'BatchMode yes' %s@%s -p %s -i %s '%s'",
			machine.User,
			machine.Address,
			machine.Port,
			a.path+"/keys/"+machine.PrivateKey,
			action.Command,
		)
		cmd = exec.Command("/bin/bash", "-c", sshCommand)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

/*
Get the output stored locally in the log with the given id
*/
func (a *Actions) GetLogOutput(logId string) {
	// If the log id given is not full, search for the first log that
	// matches the id prefix
	if len(logId) < 16 {
		match := ""
		logs, err := loadLogs(a.path)
		if err != nil {
			fmt.Println("ERROR: " + err.Error())
		}

		for _, log := range logs {
			if strings.HasPrefix(log.Id, logId) {
				match = log.Id
				break
			}
		}

		// If no match, inform the user
		if match == "" {
			fmt.Println("ERROR: Log not found")
			return
		}

		logId = match

	}
	t, err := tail.TailFile(a.path+"/logs/"+logId, tail.Config{Follow: true})
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
		return
	}

	for line := range t.Lines {
		if line.Text == "-----Finished-----" || line.Text == "-----Error-----" {
			break
		}
		fmt.Println(line.Text)
	}
}

/*
Interactive ssh
*/
func (a *Actions) SSH(machineId string) error {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	var machine Machine
	found := false
	for _, m := range setup.Machines {
		if m.Id == machineId {
			machine = m
			found = true
			break
		}
	}

	// Check if no machine matched
	if !found {
		return errors.New("No machine with the given id was found")
	}

	sshCommand := fmt.Sprintf(
		"ssh -tt -o 'StrictHostKeyChecking no' -o 'BatchMode yes' %s@%s -p %s -i %s",
		machine.User,
		machine.Address,
		machine.Port,
		a.path+"/keys/"+machine.PrivateKey,
	)
	cmd := exec.Command("/bin/bash", "-c", sshCommand)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}


/*
Copy files/directories from one machine to another
*/
func (a *Actions) SCP(from, to string) error {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	// Figure out which is local and which is remote
	fromParts := strings.Split(from, ":")
	toParts := strings.Split(to, ":")

	var machineId string
	var localToRemote bool

	if len(fromParts) == 1 && len(toParts) == 2 { // Local to remote
		localToRemote = true
		machineId = toParts[0]
	} else if len(fromParts) == 2 && len(toParts) == 1 { // Remote to local
		localToRemote = false
		machineId = fromParts[0]
	} else { // Invalid
		return errors.New("Invalid arguments to scp")
	}

	var machine Machine
	found := false
	for _, m := range setup.Machines {
		if m.Id == machineId {
			machine = m
			found = true
			break
		}
	}

	// Check if no machine matched
	if !found {
		return errors.New("No machine with the given id was found")
	}

	// Build the from / to strings
	var fromString string
	var toString string

	remoteString := fmt.Sprintf(
		"%s@%s:",
		machine.User,
		machine.Address,
	)

	if localToRemote {
		fromString = from
		toString = remoteString + toParts[1]
	} else {
		fromString = remoteString + fromParts[1]
		toString = to
	}

	// Build and execute the command
	scpCommand := fmt.Sprintf(
		"scp -o 'StrictHostKeyChecking no' -o 'BatchMode yes' -i %s -P %s -r %s %s",
		a.path+"/keys/"+machine.PrivateKey,
		machine.Port,
		fromString,
		toString,
	)
	cmd := exec.Command("/bin/bash", "-c", scpCommand)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

/*
Mount SSHfs
*/
func (a *Actions) Mount(machineId string,remoteMountPoint string,localMountPoint string) error {
	setup, err := loadSetup(a.path)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
	}

	var machine Machine
	found := false
	for _, m := range setup.Machines {
		if m.Id == machineId {
			machine = m
			found = true
			break
		}
	}

	// Check if no machine matched
	if !found {
		return errors.New("No machine with the given id was found")
	}

        commandString := fmt.Sprintf(
                "sshfs %s@%s:%s %s -p %s -o IdentityFile=%s -o sshfs_sync",
		machine.User,
		machine.Address,
                remoteMountPoint,
                localMountPoint,
		machine.Port,
		a.path+"/keys/"+machine.PrivateKey,
	)
	cmd := exec.Command("/bin/bash", "-c", commandString)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func (a *Actions) Unmount(localpath string) error{
        commandString := fmt.Sprintf("fusermount -u %s", localpath)

	cmd := exec.Command("/bin/bash", "-c", commandString)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
