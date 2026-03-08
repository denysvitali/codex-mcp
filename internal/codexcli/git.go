package codexcli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var errGitNotFound = errors.New("git not found in PATH")

func isGitRepo(ctx context.Context, cwd string) (bool, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return false, fmt.Errorf("%w: %v", errGitNotFound, err)
		}
		return false, fmt.Errorf("find git in PATH: %w", err)
	}

	cmd := exec.CommandContext(ctx, gitPath, "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(string(output), "not a git repository") {
			return false, nil
		}
		return false, err
	}
	return string(output) == "true\n", nil
}
