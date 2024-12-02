package replace

import (
	"os"
	"os/exec"
	"syscall"
)

func Exec(binary string, args ...string) {
	var err error
	binary, err = exec.LookPath(binary)
	if err != nil {
		panic(err)
	}

	// get the system's environment variables
	environment := os.Environ()

	// get a slice of the pieces of the command
	command := append([]string{binary}, args...)

	err = syscall.Exec(binary, command, environment)
	if err != nil {
		panic(err)
	}
}
