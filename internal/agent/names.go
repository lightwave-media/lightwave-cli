package agent

// BranchName returns the git branch name for an agent.
func BranchName(agentName string) string {
	return "lw/" + agentName
}
