package simplecobra

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Commander is the interface that must be implemented by all commands.
type Commander interface {
	// The name of the command.
	Name() string

	// The command execution.
	Run(ctx context.Context, args []string) error

	// Init called on all commands in this tree, before execution, starting from the root.
	// This is the place to evaluate flags and set up the command.
	Init(*Commandeer) error

	// WithCobraCommand is called when the cobra command is created.
	// This is where the flags, short and long description etc. are added.
	WithCobraCommand(*cobra.Command) error

	// Commands returns the sub commands, if any.
	Commands() []Commander
}

// New creates a new Executer from the command tree in Commander.
func New(rootCmd Commander) (*Exec, error) {
	rootCd := &Commandeer{
		Command: rootCmd,
	}
	rootCd.Root = rootCd

	// Add all commands recursively.
	var addCommands func(cd *Commandeer, cmd Commander)
	addCommands = func(cd *Commandeer, cmd Commander) {
		cd2 := &Commandeer{
			Root:    rootCd,
			Parent:  cd,
			Command: cmd,
		}
		cd.commandeers = append(cd.commandeers, cd2)
		for _, c := range cmd.Commands() {
			addCommands(cd2, c)
		}

	}

	for _, cmd := range rootCmd.Commands() {
		addCommands(rootCd, cmd)
	}

	if err := rootCd.compile(); err != nil {
		return nil, err
	}

	return &Exec{c: rootCd}, nil

}

// Commandeer holds the state of a command and its subcommands.
type Commandeer struct {
	Command      Commander
	CobraCommand *cobra.Command

	Root        *Commandeer
	Parent      *Commandeer
	commandeers []*Commandeer
}

func (c *Commandeer) init() error {

	// Collect all ancestors including self.
	var ancestors []*Commandeer
	{
		cd := c
		for cd != nil {
			ancestors = append(ancestors, cd)
			cd = cd.Parent
		}
	}

	// Init all of them starting from the root.
	for i := len(ancestors) - 1; i >= 0; i-- {
		cd := ancestors[i]
		if err := cd.Command.Init(cd); err != nil {
			return err
		}
	}

	return nil

}

type runErr struct {
	err error
}

func (r *runErr) Error() string {
	return fmt.Sprintf("run error: %v", r.err)
}

func (c *Commandeer) compile() error {
	c.CobraCommand = &cobra.Command{
		Use: c.Command.Name(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.Command.Run(cmd.Context(), args); err != nil {
				return &runErr{err: err}
			}
			return nil
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return c.init()
		},
		SilenceErrors:              true,
		SilenceUsage:               true,
		SuggestionsMinimumDistance: 2,
	}

	// This is where the flags, short and long description etc. are added
	c.Command.WithCobraCommand(c.CobraCommand)

	// Add commands recursively.
	for _, cc := range c.commandeers {
		if err := cc.compile(); err != nil {
			return err
		}
		c.CobraCommand.AddCommand(cc.CobraCommand)
	}

	return nil
}

// Exec provides methods to execute the command tree.
type Exec struct {
	c *Commandeer
}

// Execute executes the command tree starting from the root command.
// The args are usually filled with os.Args[1:].
func (r *Exec) Execute(ctx context.Context, args []string) (*Commandeer, error) {
	r.c.CobraCommand.SetArgs(args)
	cobraCommand, err := r.c.CobraCommand.ExecuteContextC(ctx)
	var cd *Commandeer
	if cobraCommand != nil {
		if err == nil {
			err = checkArgs(cobraCommand, args)
		}

		// Find the commandeer that was executed.
		var find func(*cobra.Command, *Commandeer) *Commandeer
		find = func(what *cobra.Command, in *Commandeer) *Commandeer {
			if in.CobraCommand == what {
				return in
			}
			for _, in2 := range in.commandeers {
				if found := find(what, in2); found != nil {
					return found
				}
			}
			return nil
		}
		cd = find(cobraCommand, r.c)
	}

	return cd, wrapErr(err)
}

// CommandError is returned when a command fails because of a user error (unknown command, invalid flag etc.).
type CommandError struct {
	Err error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("command error: %v", e.Err)
}

// IsCommandError  reports whether any error in err's tree matches CommandError.
func IsCommandError(err error) bool {
	switch err.(type) {
	case *CommandError:
		return true
	}
	return errors.Is(err, &CommandError{})
}

func wrapErr(err error) error {
	if err == nil {
		return nil
	}

	if rerr, ok := err.(*runErr); ok {
		err = rerr.err
	}

	// All other errors are coming from Cobra.
	return &CommandError{Err: err}
}

// Cobra only does suggestions for the root command.
// See https://github.com/spf13/cobra/pull/1500
func checkArgs(cmd *cobra.Command, args []string) error {
	// no subcommand, always take args.
	if !cmd.HasSubCommands() {
		return nil
	}

	var commandName string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}
		commandName = arg
	}

	if commandName == "" || cmd.Name() == commandName {
		return nil
	}

	return fmt.Errorf("unknown command %q for %q%s", args[0], cmd.CommandPath(), findSuggestions(cmd, commandName))
}

func findSuggestions(cmd *cobra.Command, arg string) string {
	if cmd.DisableSuggestions {
		return ""
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	suggestionsString := ""
	if suggestions := cmd.SuggestionsFor(arg); len(suggestions) > 0 {
		suggestionsString += "\n\nDid you mean this?\n"
		for _, s := range suggestions {
			suggestionsString += fmt.Sprintf("\t%v\n", s)
		}
	}
	return suggestionsString
}
