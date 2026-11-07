package cli

import (
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// timeFormat is how the CLI shows times, in the local zone.
const timeFormat = "2006-01-02 15:04"

func newProject() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Create, list and delete projects"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "create NAME",
			Short: "Create a project",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				lab, err := connect()
				if err != nil {
					return err
				}
				project, err := lab.CreateProject(args[0])
				if err != nil {
					return err
				}
				cmd.Printf("project %s created (ID %d)\n", project.Name, project.ID)
				return nil
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List the projects",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				lab, err := connect()
				if err != nil {
					return err
				}
				projects, err := lab.Projects()
				if err != nil {
					return err
				}
				rows := [][]string{{"ID", "NAME", "INTERNET", "CREATED"}}
				for _, project := range projects {
					rows = append(rows, []string{strconv.FormatInt(project.ID, 10), project.Name, yesNo(project.Internet), localTime(project.Created)})
				}
				return printTable(cmd, rows)
			},
		},
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Delete a project with everything in it",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				lab, err := connect()
				if err != nil {
					return err
				}
				if err := lab.DeleteProject(args[0]); err != nil {
					return err
				}
				cmd.Printf("project %s deleted\n", args[0])
				return nil
			},
		},
	)
	return cmd
}

func newToken() *cobra.Command {
	var project, permission string
	cmd := &cobra.Command{Use: "token", Short: "Create, list and delete API tokens of a project"}
	cmd.PersistentFlags().StringVar(&project, "project", "default", "the project")
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a token and print it (only now)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lab, err := connect()
			if err != nil {
				return err
			}
			created, err := lab.CreateToken(project, args[0], permission)
			if err != nil {
				return err
			}
			cmd.Println(created.Secret)
			return nil
		},
	}
	create.Flags().StringVar(&permission, "permission", "read_write", "read or read_write")
	cmd.AddCommand(
		create,
		&cobra.Command{
			Use:   "list",
			Short: "List the tokens of a project",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				lab, err := connect()
				if err != nil {
					return err
				}
				tokens, err := lab.Tokens(project)
				if err != nil {
					return err
				}
				rows := [][]string{{"ID", "NAME", "PERMISSION", "STARTS WITH", "CREATED"}}
				for _, token := range tokens {
					rows = append(rows, []string{strconv.FormatInt(token.ID, 10), token.Name, token.Permission, token.Prefix + "…", localTime(token.Created)})
				}
				return printTable(cmd, rows)
			},
		},
		&cobra.Command{
			Use:   "delete NAME",
			Short: "Delete a token of a project",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				lab, err := connect()
				if err != nil {
					return err
				}
				if err := lab.DeleteToken(project, args[0]); err != nil {
					return err
				}
				cmd.Printf("token %s deleted\n", args[0])
				return nil
			},
		},
	)
	return cmd
}

// yesNo shows a flag in a list.
func yesNo(flag bool) string {
	if flag {
		return "yes"
	}
	return "no"
}

// localTime shows a time in the local zone.
func localTime(at time.Time) string {
	return at.Local().Format(timeFormat)
}
