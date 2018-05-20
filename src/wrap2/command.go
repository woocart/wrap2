package main

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// Command holds data about process to be executed
type Command struct {
	Command  string   `toml:"cmd"`
	Template Template `toml:"config, omitempty"`
	RunAs    string   `toml:"user, omitempty"`
	uid      int
	gid      int
	user     string
}

// RunBlocking runs command in blocking mode
func (c *Command) RunBlocking() {
	args := strings.Split(c.Command, " ")
	process := exec.Command(args[0], args[1:]...)

	if c.RunAs != "" {
		currentUser, err := user.Lookup(c.RunAs)
		if err != nil {
			log.Fatal(
				"Failed getting user",
				zap.String("run_as", c.RunAs),
				zap.String("cmd", c.Command),
				zap.Error(err),
			)
		}
		c.user = currentUser.Username
		c.uid, _ = strconv.Atoi(currentUser.Uid)
		c.gid, _ = strconv.Atoi(currentUser.Gid)
	} else {
		currentUser, err := user.Current()
		if err != nil {
			log.Fatal(
				"Failed getting user",
				zap.String("run_as", c.RunAs),
				zap.String("cmd", c.Command),
				zap.Error(err),
			)
		}
		c.user = currentUser.Username
		c.uid, _ = strconv.Atoi(currentUser.Uid)
		c.gid, _ = strconv.Atoi(currentUser.Gid)
	}

	process.SysProcAttr = &syscall.SysProcAttr{}
	process.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(c.uid), Gid: uint32(c.gid)}

	process.Stdin = os.Stdin
	log.Info("Starting", zap.Strings("args", args), zap.String("user", c.user))
	_, err := process.CombinedOutput()
	if err != nil {
		log.Fatal(
			"Failed starting command",
			zap.String("cmd", c.Command),
			zap.Error(err),
		)
	}
}

// Run executes process and redirects pipes
func (c *Command) Run(ctx context.Context, cancel context.CancelFunc) {
	go func(command, runAs string) {
		defer wg.Done()
		args := strings.Split(command, " ")
		process := exec.Command(args[0], args[1:]...)
		if runAs != "" {
			currentUser, err := user.Lookup(runAs)
			if err != nil {
				log.Fatal(
					"Failed getting user",
					zap.String("run_as", runAs),
					zap.String("cmd", c.Command),
					zap.Error(err),
				)
			}
			c.user = currentUser.Username
			c.uid, _ = strconv.Atoi(currentUser.Uid)
			c.gid, _ = strconv.Atoi(currentUser.Gid)
		} else {
			currentUser, err := user.Current()
			if err != nil {
				log.Fatal(
					"Failed getting user",
					zap.String("run_as", c.RunAs),
					zap.String("cmd", c.Command),
					zap.Error(err),
				)
			}
			c.user = currentUser.Username
			c.uid, _ = strconv.Atoi(currentUser.Uid)
			c.gid, _ = strconv.Atoi(currentUser.Gid)
		}

		process.SysProcAttr = &syscall.SysProcAttr{}
		process.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(c.uid), Gid: uint32(c.gid)}
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		process.Stdin = os.Stdin
		log.Info("Starting", zap.Strings("args", args), zap.String("user", c.user))

		// start the process
		err := process.Start()
		if err != nil {
			log.Error(
				"Failed starting command",
				zap.String("cmd", command),
				zap.Error(err),
			)
		}

		// Setup signaling
		catch := make(chan os.Signal, 1)
		signal.Notify(catch, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)

		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sig := <-catch:
				log.Info(
					"Terminating",
					zap.String("cmd", command),
					zap.String("signal", sig.String()),
				)
				signalProcessWithTimeout(process, sig)
				cancel()
			case <-ctx.Done():
				// exit when context is done
			}
		}()

		err = process.Wait()
		cancel()

		if err != nil {
			log.Info(
				"Command terminated",
				zap.String("cmd", command),
				zap.Error(err),
			)
			// OPTIMIZE: This could be cleaner
			os.Exit(err.(*exec.ExitError).Sys().(syscall.WaitStatus).ExitStatus())
		}
	}(c.Command, c.RunAs)
}

func signalProcessWithTimeout(process *exec.Cmd, sig os.Signal) {
	done := make(chan bool)
	go func() {
		process.Process.Signal(syscall.SIGINT)
		process.Process.Signal(syscall.SIGTERM)
		process.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(5 * time.Second):
		log.Info(
			"Command termianted due to timeout",
			zap.String("cmd", process.Path),
		)
		process.Process.Kill()
	}
}
