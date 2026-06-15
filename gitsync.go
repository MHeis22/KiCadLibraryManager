package main

import (
	"fmt"
	"strings"
)

// isGitRepository returns true if the path is inside a git repository.
func isGitRepository(path string) bool {
	return gitCommand("-C", path, "rev-parse", "--git-dir").Run() == nil
}

// ValidateGitURL runs git ls-remote to confirm the URL is a reachable Git remote.
func ValidateGitURL(url string) error {
	out, err := gitCommand("ls-remote", url).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// GitClone downloads a remote repository into the target directory.
func GitClone(url string, destPath string) error {
	fmt.Printf("--> Cloning repository: %s\n", url)
	out, err := gitCommand("clone", url, destPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// GitPull stashes any manual KiCad edits, runs git pull --rebase, then restores
// the edits — so a user editing symbols/footprints directly in KiCad doesn't
// block syncing. Returns nil for non-git repos (silently skipped).
func GitPull(repoPath string) error {
	if !isGitRepository(repoPath) {
		return nil
	}

	// 1. Detect manual edits made directly in KiCad (uncommitted changes).
	statusOut, _ := gitCommand("-C", repoPath, "status", "--porcelain").Output()
	hasManualEdits := strings.TrimSpace(string(statusOut)) != ""

	// 2. Stash them safely before pulling.
	if hasManualEdits {
		fmt.Println("    [Git] Stashing manual KiCad edits...")
		gitCommand("-C", repoPath, "stash").Run()
	}

	// 3. Pull the latest from the remote.
	out, err := gitCommand("-C", repoPath, "pull", "--rebase").CombinedOutput()

	// 4. Always restore the manual edits.
	if hasManualEdits {
		fmt.Println("    [Git] Restoring manual KiCad edits...")
		if popErr := gitCommand("-C", repoPath, "stash", "pop").Run(); popErr != nil {
			fmt.Println("    [Git Error] Conflict restoring manual edits. Favoring local changes to protect the KiCad library.")
			// Keep local (stashed) changes to avoid S-expression corruption.
			gitCommand("-C", repoPath, "checkout", "--ours", ".").Run()
			gitCommand("-C", repoPath, "stash", "drop").Run()
		}
	}

	if err != nil {
		return fmt.Errorf("pull failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// GitCommitAndPush stages all changes, commits, and pushes to remote.
// Returns (pushed=true, nil) on success or when there is nothing to commit.
// Returns (pushed=false, nil) if push was rejected by remote (signal for retry).
// Returns (false, non-nil) on hard failures (add or commit errors).
// Returns (true, nil) for non-git repos.
func GitCommitAndPush(repoPath, commitMessage string) (bool, error) {
	if !isGitRepository(repoPath) {
		return true, nil
	}

	if err := gitCommand("-C", repoPath, "add", ".").Run(); err != nil {
		return false, fmt.Errorf("git add failed: %w", err)
	}

	commitOut, commitErr := gitCommand("-C", repoPath, "commit", "-m", commitMessage).CombinedOutput()
	if commitErr != nil {
		if strings.Contains(string(commitOut), "nothing to commit") {
			fmt.Println("    [Git] Nothing to commit.")
			return true, nil
		}
		return false, fmt.Errorf("git commit failed: %s", strings.TrimSpace(string(commitOut)))
	}
	fmt.Printf("    [Git] Committed: %q\n", commitMessage)

	if err := gitCommand("-C", repoPath, "push").Run(); err != nil {
		fmt.Println("    [Git] Push rejected by remote — will retry.")
		return false, nil
	}

	fmt.Println("    [Git] Successfully synchronized with remote repository.")
	return true, nil
}

// GitResetLastCommit hard-resets the working directory to HEAD~1, undoing the last commit.
func GitResetLastCommit(repoPath string) error {
	return gitCommand("-C", repoPath, "reset", "--hard", "HEAD~1").Run()
}

// GitFetchAndCheckStatus fetches from remote and returns whether the local branch
// is behind its upstream tracking ref.
func GitFetchAndCheckStatus(repoPath string) (behind bool, err error) {
	if !isGitRepository(repoPath) {
		return false, nil
	}

	// Non-destructive: updates remote tracking refs without touching the working tree
	gitCommand("-C", repoPath, "fetch", "--quiet").Run()

	// Count commits the upstream has that we don't. Comparing HEAD != @{u} would
	// also flag a repo that is merely *ahead* (unpushed local commits) as behind.
	out, err := gitCommand("-C", repoPath, "rev-list", "--count", "HEAD..@{u}").Output()
	if err != nil {
		return false, nil // No upstream configured / detached — not considered behind
	}

	count := strings.TrimSpace(string(out))
	return count != "" && count != "0", nil
}
