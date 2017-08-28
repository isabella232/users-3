package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/google/go-github/github"
	chart "github.com/wcharczuk/go-chart"
	"golang.org/x/oauth2"
)

type Repo struct {
	Name  string
	Stars int
	Date  time.Time
}

func init() {
	log.SetHandler(cli.New(os.Stdout))
}

func main() {
	log.Info("starting up...")
	var ctx = context.Background()
	var ts = oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	var client = github.NewClient(oauth2.NewClient(ctx, ts))
	var repos []Repo

	for _, file := range []string{"goreleaser.yml", "goreleaser.yaml"} {
		log.Infof("looking for repos with a %s file...", file)
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: 100,
			},
		}
		for {
			result, resp, err := client.Search.Code(
				ctx,
				fmt.Sprintf("filename:%s language:yaml", file),
				opts,
			)
			if _, ok := err.(*github.RateLimitError); ok {
				log.Warn("hit rate limit")
				time.Sleep(10 * time.Second)
				continue
			}
			if err != nil {
				log.WithError(err).Fatal("failed to gather results")
			}
			log.Infof("found %d results", len(result.CodeResults))
			for _, result := range result.CodeResults {
				if exists(result.Repository.GetFullName(), repos) {
					continue
				}
				repo, err := newRepo(ctx, client, result)
				if err != nil {
					log.WithField("repo", result.Repository.GetFullName()).
						WithError(err).Error("failed to get repo details")
				}
				if repo.Name == "" {
					continue
				}
				repos = append(repos, repo)
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Stars > repos[j].Stars
	})
	log.Info("")
	log.Info("")
	log.Infof("\033[1mTHERE ARE %d REPOSITORIES USING GORELEASER:\033[0m", len(repos))
	log.Info("")
	for _, repo := range repos {
		log.Infof("%s with %d stars (using since %v)", repo.Name, repo.Stars, repo.Date)
	}
	graph, err := graphRepos(repos)
	if err != nil {
		log.WithError(err).Fatal("failed to graph repos")
	}
	log.Infof("\ngraph saved at %s", graph)
}

func newRepo(ctx context.Context, client *github.Client, result github.CodeResult) (Repo, error) {
	repo, _, err := client.Repositories.Get(
		ctx,
		result.Repository.Owner.GetLogin(),
		result.Repository.GetName(),
	)
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
		return newRepo(ctx, client, result)
	}
	if err != nil {
		return Repo{}, err
	}
	if strings.HasPrefix(result.GetPath(), "/") {
		return Repo{}, nil
	}
	commits, _, err := client.Repositories.ListCommits(
		ctx,
		repo.Owner.GetLogin(),
		repo.GetName(),
		&github.CommitsListOptions{
			Path: result.GetPath(),
		},
	)
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
		return newRepo(ctx, client, result)
	}
	if err != nil || len(commits) == 0 {
		return Repo{}, err
	}
	commit := commits[len(commits)-1]
	c, _, err := client.Git.GetCommit(
		ctx,
		repo.Owner.GetLogin(),
		repo.GetName(),
		commit.GetSHA(),
	)
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
		return newRepo(ctx, client, result)
	}
	if err != nil {
		return Repo{}, err
	}

	return Repo{
		Name:  repo.GetFullName(),
		Stars: repo.GetStargazersCount(),
		Date:  c.Committer.GetDate(),
	}, nil
}

func exists(name string, rs []Repo) bool {
	for _, r := range rs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func graphRepos(repos []Repo) (string, error) {
	var filename = fmt.Sprintf("chart_%v.svg", time.Now().Format(time.RFC822))
	var series = chart.TimeSeries{Style: chart.StyleShow()}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Date.Before(repos[j].Date)
	})
	for i, repo := range repos {
		series.XValues = append(series.XValues, repo.Date)
		series.YValues = append(series.YValues, float64(i))
	}
	var graph = chart.Chart{
		XAxis: chart.XAxis{
			Name:      "Time",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
		},
		YAxis: chart.YAxis{
			Name:      "Using",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
		},
		Series: []chart.Series{series},
	}
	var buffer = bytes.NewBuffer([]byte{})
	if err := graph.Render(chart.SVG, buffer); err != nil {
		return "", err
	}
	if err := ioutil.WriteFile(filename, buffer.Bytes(), 0644); err != nil {
		return "", err
	}
	return filename, nil
}
