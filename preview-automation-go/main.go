package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-github/v66/github"
	"golang.org/x/oauth2"
)

type Config struct {
	PRNumber       int
	AppName        string
	Region         string
	BaseDomain     string
	CertificateARN string
	SourcePath     string
	Action         string // "deploy" or "cleanup"
	RepoOwner      string
	RepoName       string
}

type PreviewManager struct {
	cfg          *Config
	awsCfg       aws.Config
	s3Client     *s3.Client
	cfClient     *cloudfront.Client
	r53Client    *route53.Client
	githubClient *github.Client
	bucketName   string
	fullDomain   string
	subdomain    string
}

func main() {
	cfg := &Config{}

	flag.IntVar(&cfg.PRNumber, "pr", 0, "Pull Request number")
	flag.StringVar(&cfg.AppName, "app", "", "Application name")
	flag.StringVar(&cfg.Region, "region", "us-east-1", "AWS region")
	flag.StringVar(&cfg.BaseDomain, "domain", "", "Base domain (e.g., preview.yourapp.com)")
	flag.StringVar(&cfg.CertificateARN, "cert", "", "ACM Certificate ARN")
	flag.StringVar(&cfg.SourcePath, "source", "./dist", "Source directory to upload")
	flag.StringVar(&cfg.Action, "action", "deploy", "Action to perform: deploy or cleanup")
	flag.StringVar(&cfg.RepoOwner, "repo-owner", "", "GitHub repository owner")
	flag.StringVar(&cfg.RepoName, "repo-name", "", "GitHub repository name")
	flag.Parse()

	if cfg.PRNumber == 0 {
		log.Fatal("PR number is required (--pr)")
	}
	if cfg.AppName == "" {
		log.Fatal("App name is required (--app)")
	}
	if cfg.BaseDomain == "" {
		log.Fatal("Base domain is required (--domain)")
	}
	if cfg.RepoOwner == "" {
		log.Fatal("Repository owner is required (--repo-owner)")
	}

	ctx := context.Background()

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.Region))
	if err != nil {
		log.Fatalf("Unable to load AWS config: %v", err)
	}

	var githubClient *github.Client
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		tc := oauth2.NewClient(ctx, ts)
		githubClient = github.NewClient(tc)
	} else {
		log.Println("Warning: GITHUB_TOKEN not set, PR comment will be skipped")
	}

	bucketName := fmt.Sprintf("pr-%d-%s", cfg.PRNumber, cfg.AppName)

	pm := &PreviewManager{
		cfg:          cfg,
		awsCfg:       awsCfg,
		s3Client:     s3.NewFromConfig(awsCfg),
		cfClient:     cloudfront.NewFromConfig(awsCfg),
		r53Client:    route53.NewFromConfig(awsCfg),
		githubClient: githubClient,
		subdomain:    bucketName,
		bucketName:   bucketName,
		fullDomain:   fmt.Sprintf("%s.%s", bucketName, cfg.BaseDomain),
	}

	if cfg.Action == "cleanup" {
		if err := pm.Cleanup(ctx); err != nil {
			log.Fatalf("Cleanup failed: %v", err)
		}
		fmt.Println("Cleanup completed successfully")
	} else {
		if err := pm.Deploy(ctx); err != nil {
			log.Fatalf("Deployment failed: %v", err)
		}
		fmt.Printf("\n✓ Preview environment deployed successfully!\n")
		fmt.Printf("URL: https://%s\n", pm.fullDomain)
		fmt.Printf("Note: Initial deployment may take 3-5 minutes for CloudFront to propagate globally.\n")
	}
}

func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	contentTypes := map[string]string{
		".html": "text/html",
		".css":  "text/css",
		".js":   "application/javascript",
		".json": "application/json",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".ico":  "image/x-icon",
		".xml":  "application/xml",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
	}

	if ct, ok := contentTypes[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}
