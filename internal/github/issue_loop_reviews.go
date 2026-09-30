package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Each query has one paginated connection so gh follows that connection's
// cursor. Both queries must describe the same head as the check snapshot.
const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$endCursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    headRefOid reviewDecision
    connection:reviewThreads(first:100,after:$endCursor){
      nodes{isResolved isOutdated comments(first:1){nodes{url}}}
      pageInfo{hasNextPage endCursor}
    }
  }}
}`

const reviewOpinionsQuery = `query($owner:String!,$name:String!,$number:Int!,$endCursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    headRefOid reviewDecision
    connection:latestOpinionatedReviews(first:100,after:$endCursor){
      nodes{state url}
      pageInfo{hasNextPage endCursor}
    }
  }}
}`

type reviewNode struct {
	State      string `json:"state"`
	URL        string `json:"url"`
	IsResolved bool   `json:"isResolved"`
	Comments   struct {
		Nodes []struct {
			URL string `json:"url"`
		} `json:"nodes"`
	} `json:"comments"`
}

type reviewPage struct {
	Data struct {
		Repository struct {
			PullRequest *struct {
				HeadRefOID     string `json:"headRefOid"`
				ReviewDecision string `json:"reviewDecision"`
				Connection     *struct {
					Nodes []reviewNode `json:"nodes"`
				} `json:"connection"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// loadPullRequestReviews keeps unresolved discussions as repair
// instructions, including COMMENTED reviews such as Bugbot's. GitHub's review
// decision can be null on an unprotected branch, so read each reviewer's latest
// opinion too. A thread becoming outdated only means its diff changed; it
// remains a finding until resolved. Superseded/dismissed opinions do not keep
// sending an already-addressed finding back to the developer.
func loadPullRequestReviews(repo string, pr *PullRequest) error {
	threads, err := pullRequestReviewNodes(repo, pr, reviewThreadsQuery)
	if err != nil {
		return err
	}

	for _, thread := range threads {
		if thread.IsResolved {
			continue
		}

		url := pr.URL
		if len(thread.Comments.Nodes) > 0 {
			url = thread.Comments.Nodes[0].URL
		}

		pr.BlockingReviews = append(pr.BlockingReviews, "unresolved review thread: "+url)
	}

	opinions, err := pullRequestReviewNodes(repo, pr, reviewOpinionsQuery)
	if err != nil {
		return err
	}

	for _, opinion := range opinions {
		if opinion.State == "CHANGES_REQUESTED" {
			pr.BlockingReviews = append(pr.BlockingReviews, "changes requested: "+opinion.URL)
		}
	}

	return nil
}

func pullRequestReviewNodes(repo string, pr *PullRequest, query string) ([]reviewNode, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || pr.HeadRefOID == "" {
		return nil, fmt.Errorf("cannot read reviews for %s#%d without repository and head SHA", repo, pr.Number)
	}

	out, err := exec.Command("gh", "api", "graphql", "--paginate", "--slurp",
		"-f", "query="+query, "-F", "owner="+owner, "-F", "name="+name, "-F", "number="+strconv.Itoa(pr.Number)).Output()
	if err != nil {
		return nil, fmt.Errorf("read reviews for %s#%d: %w", repo, pr.Number, err)
	}

	var pages []reviewPage
	if err := json.Unmarshal(out, &pages); err != nil {
		return nil, fmt.Errorf("parse reviews for %s#%d: %w", repo, pr.Number, err)
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("no review pages returned for %s#%d", repo, pr.Number)
	}

	var nodes []reviewNode

	for _, page := range pages {
		snapshot := page.Data.Repository.PullRequest
		if snapshot == nil || snapshot.Connection == nil || snapshot.HeadRefOID != pr.HeadRefOID {
			return nil, fmt.Errorf("incomplete review evidence or changed head for %s#%d; retry on the next tick", repo, pr.Number)
		}

		pr.ReviewDecision = snapshot.ReviewDecision
		nodes = append(nodes, snapshot.Connection.Nodes...)
	}

	return nodes, nil
}
