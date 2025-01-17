package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: go run main.go <path-to-repo>")
		os.Exit(1)
	}

	repoPath := os.Args[1]
	runs := 5

	// Benchmark git ls-files shell command
	shellTimes := make([]time.Duration, runs)
	shellFiles := 0
	for i := 0; i < runs; i++ {
		start := time.Now()
		files, err := gitLsFilesShell(repoPath)
		if err != nil {
			fmt.Printf("Shell command error: %v\n", err)
			os.Exit(1)
		}
		shellTimes[i] = time.Since(start)
		if i == 0 {
			shellFiles = len(files)
			fmt.Printf("Shell command found %d files\n", shellFiles)
		}
	}

	// Benchmark go-git tree traversal
	goGitTimes := make([]time.Duration, runs)
	goGitFiles := 0
	for i := 0; i < runs; i++ {
		start := time.Now()
		files, err := gitLsFilesGoGit(repoPath)
		if err != nil {
			fmt.Printf("go-git error: %v\n", err)
			os.Exit(1)
		}
		goGitTimes[i] = time.Since(start)
		if i == 0 {
			goGitFiles = len(files)
			fmt.Printf("go-git found %d files\n", goGitFiles)
			if goGitFiles != shellFiles {
				fmt.Printf("Warning: File count mismatch! Shell: %d, go-git: %d\n",
					shellFiles, goGitFiles)
			}
		}
		// Print progress for long-running operations
		fmt.Printf("Completed go-git run %d/%d\n", i+1, runs)
	}

	// Calculate and print statistics
	shellAvg := calculateStats(shellTimes)
	goGitAvg := calculateStats(goGitTimes)

	fmt.Printf("\nResults over %d runs:\n", runs)
	fmt.Printf("Shell command average: %v (min: %v, max: %v)\n",
		shellAvg.avg, shellAvg.min, shellAvg.max)
	fmt.Printf("go-git average: %v (min: %v, max: %v)\n",
		goGitAvg.avg, goGitAvg.min, goGitAvg.max)

	// Calculate relative performance
	ratio := float64(goGitAvg.avg) / float64(shellAvg.avg)
	fmt.Printf("\ngo-git is %.2fx %s than shell command\n",
		ratio,
		map[bool]string{true: "slower", false: "faster"}[ratio > 1])
}

type timeStats struct {
	avg, min, max time.Duration
}

func calculateStats(times []time.Duration) timeStats {
	var total time.Duration
	min := times[0]
	max := times[0]

	for _, t := range times {
		total += t
		if t < min {
			min = t
		}
		if t > max {
			max = t
		}
	}

	return timeStats{
		avg: total / time.Duration(len(times)),
		min: min,
		max: max,
	}
}

func gitLsFilesShell(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(output)), "\n"), nil
}

func gitLsFilesGoGit(repoPath string) ([]string, error) {
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return nil, err
	}

	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("error getting HEAD: %w", err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("error getting commit: %w", err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("error getting tree: %w", err)
	}

	var files []string
	err = tree.Files().ForEach(func(f *object.File) error {
		files = append(files, f.Name)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking tree: %w", err)
	}

	return files, nil
}
