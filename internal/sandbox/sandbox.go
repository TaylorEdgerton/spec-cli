package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/TaylorEdgerton/spec-cli/internal/secrets"
)

func Available() bool {
	_, err := exec.LookPath("sbx")
	return err == nil
}

func Launch(root, agent string, assumeYes bool, input io.Reader, output io.Writer) error {
	findings, err := secrets.Scan(root)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		fmt.Fprintln(output, "Potential AI-readable sensitive files:")
		fmt.Fprintln(output)
		for _, finding := range findings {
			fmt.Fprintf(output, "  %-28s %s\n", finding.Path, finding.Reason)
		}
		fmt.Fprintln(output)
		fmt.Fprintln(output, "These files are inside the workspace exposed to the sandbox.")
		fmt.Fprintln(output, "Gitignored files remain readable by tools inside that workspace.")
		fmt.Fprintln(output, "This check does not find all secrets.")
		if !assumeYes {
			fmt.Fprintln(output)
			fmt.Fprint(output, "Continue? [y/N] ")
			var answer string
			_, _ = fmt.Fscanln(input, &answer)
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				return fmt.Errorf("sandbox launch cancelled")
			}
		}
	}
	if !Available() {
		return fmt.Errorf("Docker Sandbox CLI `sbx` is not available")
	}
	if agent == "" {
		agent = "shell"
	}
	cmd := exec.Command("sbx", "run", agent, root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
