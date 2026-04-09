package main

import (
	"fmt"
	"os/exec"
)

// GitClone downloads a remote repository into the target directory
func GitClone(url string, destPath string) error {
	fmt.Printf("--> Cloning repository: %s\n", url)
	cmd := exec.Command("git", "clone", url, destPath)
	return cmd.Run()
}

// GitSmartSync executes the Stash -> Pull (Rebase) -> Pop -> Add -> Commit -> Push flow
func GitSmartSync(repoPath string, commitMessage string) {
	fmt.Println("--> Initiating Smart Git Sync for:", repoPath)

	// 1. Check if it's a git repo
	if err := exec.Command("git", "-C", repoPath, "status").Run(); err != nil {
		fmt.Println("    [Git] Not a git repository. Skipping sync.")
		return
	}

	// 2. Stash any uncommitted changes (like the part we just generated)
	exec.Command("git", "-C", repoPath, "stash").Run()

	// 3. Pull with rebase to safely apply teammates' changes first
	if err := exec.Command("git", "-C", repoPath, "pull", "--rebase").Run(); err != nil {
		fmt.Println("    [Git Warning] Pull failed (Offline?). Proceeding with local commit.")
	}

	// 4. Pop the stash to put our new part back on top
	exec.Command("git", "-C", repoPath, "stash", "pop").Run()

	// 5. Add all changes
	if err := exec.Command("git", "-C", repoPath, "add", ".").Run(); err != nil {
		fmt.Println("    [Git Error] Failed to 'git add':", err)
		return
	}

	// 6. Commit changes
	if err := exec.Command("git", "-C", repoPath, "commit", "-m", commitMessage).Run(); err != nil {
		fmt.Println("    [Git] No changes to commit.")
		return
	}
	fmt.Printf("    [Git] Committed changes: \"%s\"\n", commitMessage)

	// 7. Push to remote
	if err := exec.Command("git", "-C", repoPath, "push").Run(); err != nil {
		fmt.Println("    [Git Warning] Failed to push. Changes are saved locally.")
		return
	}

	fmt.Println("    [Git] Successfully synchronized with remote repository!")
}
