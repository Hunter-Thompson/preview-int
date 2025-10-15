package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-github/v66/github"
)

func (pm *PreviewManager) Deploy(ctx context.Context) error {
	fmt.Println("Starting deployment...")

	if err := pm.createS3Bucket(ctx); err != nil {
		return fmt.Errorf("failed to create S3 bucket: %w", err)
	}

	if err := pm.syncFilesToS3(ctx); err != nil {
		return fmt.Errorf("failed to sync files to S3: %w", err)
	}

	oacID, err := pm.getOrCreateOAC(ctx)
	if err != nil {
		return fmt.Errorf("failed to manage OAC: %w", err)
	}

	distributionID, err := pm.getOrCreateCloudFrontDistribution(ctx, oacID)
	if err != nil {
		return fmt.Errorf("failed to manage CloudFront distribution: %w", err)
	}

	if err := pm.setBucketPolicyForOAC(ctx, distributionID); err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}

	if err := pm.invalidateCloudFrontCache(ctx, distributionID); err != nil {
		return fmt.Errorf("failed to invalidate CloudFront cache: %w", err)
	}

	if err := pm.updateRoute53(ctx, distributionID); err != nil {
		return fmt.Errorf("failed to update Route53: %w", err)
	}

	if err := pm.postGitHubComment(ctx); err != nil {
		fmt.Printf("Warning: Failed to post GitHub comment: %v\n", err)
	}

	return nil
}

func (pm *PreviewManager) createS3Bucket(ctx context.Context) error {
	fmt.Printf("Creating S3 bucket: %s\n", pm.bucketName)

	_, err := pm.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(pm.bucketName),
	})

	if err == nil {
		fmt.Println("  ✓ Bucket already exists")
		return nil
	}

	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(pm.bucketName),
	}

	if pm.cfg.Region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(pm.cfg.Region),
		}
	}

	_, err = pm.s3Client.CreateBucket(ctx, createInput)
	if err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}

	fmt.Println("  ✓ Bucket created")
	return nil
}

func (pm *PreviewManager) syncFilesToS3(ctx context.Context) error {
	fmt.Printf("Syncing files from %s to S3...\n", pm.cfg.SourcePath)

	fileCount := 0
	err := filepath.Walk(pm.cfg.SourcePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(pm.cfg.SourcePath, path)
		if err != nil {
			return err
		}

		s3Key := filepath.ToSlash(relPath)

		contentType := getContentType(path)

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		_, err = pm.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(pm.bucketName),
			Key:         aws.String(s3Key),
			Body:        strings.NewReader(string(data)),
			ContentType: aws.String(contentType),
		})
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", s3Key, err)
		}

		fileCount++
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("  ✓ Uploaded %d files\n", fileCount)
	return nil
}

func (pm *PreviewManager) getOrCreateOAC(ctx context.Context) (string, error) {
	fmt.Println("Managing Origin Access Control...")

	oacName := fmt.Sprintf("OAC-%s", pm.bucketName)

	listResult, err := pm.cfClient.ListOriginAccessControls(ctx, &cloudfront.ListOriginAccessControlsInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list OACs: %w", err)
	}

	if listResult.OriginAccessControlList != nil && listResult.OriginAccessControlList.Items != nil {
		for _, oac := range listResult.OriginAccessControlList.Items {
			if *oac.Name == oacName {
				fmt.Printf("  ✓ Using existing OAC: %s\n", *oac.Id)
				return *oac.Id, nil
			}
		}
	}

	fmt.Println("  Creating new Origin Access Control...")
	createResult, err := pm.cfClient.CreateOriginAccessControl(ctx, &cloudfront.CreateOriginAccessControlInput{
		OriginAccessControlConfig: &cftypes.OriginAccessControlConfig{
			Name:                          aws.String(oacName),
			Description:                   aws.String(fmt.Sprintf("OAC for PR #%d preview environment", pm.cfg.PRNumber)),
			SigningProtocol:               cftypes.OriginAccessControlSigningProtocolsSigv4,
			SigningBehavior:               cftypes.OriginAccessControlSigningBehaviorsAlways,
			OriginAccessControlOriginType: cftypes.OriginAccessControlOriginTypesS3,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create OAC: %w", err)
	}

	oacID := *createResult.OriginAccessControl.Id
	fmt.Printf("  ✓ OAC created: %s\n", oacID)
	return oacID, nil
}

func (pm *PreviewManager) setBucketPolicyForOAC(ctx context.Context, distributionID string) error {
	fmt.Println("Setting bucket policy for CloudFront OAC access...")

	dist, err := pm.cfClient.GetDistribution(ctx, &cloudfront.GetDistributionInput{
		Id: aws.String(distributionID),
	})
	if err != nil {
		return fmt.Errorf("failed to get distribution: %w", err)
	}

	distributionARN := *dist.Distribution.ARN

	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Sid": "AllowCloudFrontServicePrincipal",
				"Effect": "Allow",
				"Principal": {
					"Service": "cloudfront.amazonaws.com"
				},
				"Action": "s3:GetObject",
				"Resource": "arn:aws:s3:::%s/*",
				"Condition": {
					"StringEquals": {
						"AWS:SourceArn": "%s"
					}
				}
			}
		]
	}`, pm.bucketName, distributionARN)

	_, err = pm.s3Client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(pm.bucketName),
		Policy: aws.String(policy),
	})
	if err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}

	fmt.Println("  ✓ Bucket policy configured for CloudFront access")
	return nil
}

func (pm *PreviewManager) getOrCreateCloudFrontDistribution(ctx context.Context, oacID string) (string, error) {
	fmt.Println("Managing CloudFront distribution...")

	distributionID, err := pm.findCloudFrontDistribution(ctx)
	if err != nil {
		return "", err
	}

	if distributionID != "" {
		fmt.Printf("  ✓ Using existing distribution: %s\n", distributionID)
		return distributionID, nil
	}

	return pm.createCloudFrontDistribution(ctx, oacID)
}

func (pm *PreviewManager) findCloudFrontDistribution(ctx context.Context) (string, error) {
	result, err := pm.cfClient.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list distributions: %w", err)
	}

	if result.DistributionList == nil || result.DistributionList.Items == nil {
		return "", nil
	}

	for _, dist := range result.DistributionList.Items {
		if dist.Aliases != nil && dist.Aliases.Items != nil {
			for _, alias := range dist.Aliases.Items {
				if alias == pm.fullDomain {
					return *dist.Id, nil
				}
			}
		}
	}

	return "", nil
}

func (pm *PreviewManager) createCloudFrontDistribution(ctx context.Context, oacID string) (string, error) {
	fmt.Println("  Creating new CloudFront distribution...")

	s3DomainName := fmt.Sprintf("%s.s3.%s.amazonaws.com", pm.bucketName, pm.cfg.Region)
	callerReference := fmt.Sprintf("pr-%d-%d", pm.cfg.PRNumber, time.Now().Unix())

	input := &cloudfront.CreateDistributionInput{
		DistributionConfig: &cftypes.DistributionConfig{
			CallerReference: aws.String(callerReference),
			Comment:         aws.String(fmt.Sprintf("PR #%d Preview Environment", pm.cfg.PRNumber)),
			Enabled:         aws.Bool(true),
			Aliases: &cftypes.Aliases{
				Quantity: aws.Int32(1),
				Items:    []string{pm.fullDomain},
			},
			DefaultRootObject: aws.String("index.html"),
			Origins: &cftypes.Origins{
				Quantity: aws.Int32(1),
				Items: []cftypes.Origin{
					{
						Id:         aws.String(fmt.Sprintf("S3-%s", pm.bucketName)),
						DomainName: aws.String(s3DomainName),
						S3OriginConfig: &cftypes.S3OriginConfig{
							OriginAccessIdentity: aws.String(""),
						},
						OriginAccessControlId: aws.String(oacID),
					},
				},
			},
			DefaultCacheBehavior: &cftypes.DefaultCacheBehavior{
				TargetOriginId:       aws.String(fmt.Sprintf("S3-%s", pm.bucketName)),
				ViewerProtocolPolicy: cftypes.ViewerProtocolPolicyRedirectToHttps,
				AllowedMethods: &cftypes.AllowedMethods{
					Quantity: aws.Int32(2),
					Items:    []cftypes.Method{cftypes.MethodGet, cftypes.MethodHead},
					CachedMethods: &cftypes.CachedMethods{
						Quantity: aws.Int32(2),
						Items:    []cftypes.Method{cftypes.MethodGet, cftypes.MethodHead},
					},
				},
				ForwardedValues: &cftypes.ForwardedValues{
					QueryString: aws.Bool(false),
					Cookies: &cftypes.CookiePreference{
						Forward: cftypes.ItemSelectionNone,
					},
				},
				MinTTL:     aws.Int64(0),
				DefaultTTL: aws.Int64(86400),
				MaxTTL:     aws.Int64(31536000),
				Compress:   aws.Bool(true),
				TrustedSigners: &cftypes.TrustedSigners{
					Enabled:  aws.Bool(false),
					Quantity: aws.Int32(0),
				},
			},
			CustomErrorResponses: &cftypes.CustomErrorResponses{
				Quantity: aws.Int32(1),
				Items: []cftypes.CustomErrorResponse{
					{
						ErrorCode:          aws.Int32(404),
						ResponsePagePath:   aws.String("/index.html"),
						ResponseCode:       aws.String("200"),
						ErrorCachingMinTTL: aws.Int64(300),
					},
				},
			},
		},
	}

	if pm.cfg.CertificateARN != "" {
		input.DistributionConfig.ViewerCertificate = &cftypes.ViewerCertificate{
			ACMCertificateArn:      aws.String(pm.cfg.CertificateARN),
			SSLSupportMethod:       cftypes.SSLSupportMethodSniOnly,
			MinimumProtocolVersion: cftypes.MinimumProtocolVersionTLSv132025,
		}
	} else {
		input.DistributionConfig.ViewerCertificate = &cftypes.ViewerCertificate{
			CloudFrontDefaultCertificate: aws.Bool(true),
		}
	}

	result, err := pm.cfClient.CreateDistribution(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to create distribution: %w", err)
	}

	distributionID := *result.Distribution.Id
	fmt.Printf("  ✓ Distribution created: %s\n", distributionID)

	return distributionID, nil
}

func (pm *PreviewManager) invalidateCloudFrontCache(ctx context.Context, distributionID string) error {
	fmt.Println("Invalidating CloudFront cache...")

	_, err := pm.cfClient.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(distributionID),
		InvalidationBatch: &cftypes.InvalidationBatch{
			CallerReference: aws.String(fmt.Sprintf("invalidation-%d", time.Now().Unix())),
			Paths: &cftypes.Paths{
				Quantity: aws.Int32(1),
				Items:    []string{"/*"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create invalidation: %w", err)
	}

	fmt.Println("  ✓ Cache invalidation created")
	return nil
}

func (pm *PreviewManager) updateRoute53(ctx context.Context, distributionID string) error {
	fmt.Println("Updating Route53 DNS records...")

	hostedZoneID, err := pm.getHostedZoneID(ctx)
	if err != nil {
		return err
	}

	dist, err := pm.cfClient.GetDistribution(ctx, &cloudfront.GetDistributionInput{
		Id: aws.String(distributionID),
	})
	if err != nil {
		return fmt.Errorf("failed to get distribution: %w", err)
	}

	cfDomain := *dist.Distribution.DomainName

	_, err = pm.r53Client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action: r53types.ChangeActionUpsert,
					ResourceRecordSet: &r53types.ResourceRecordSet{
						Name: aws.String(pm.fullDomain),
						Type: r53types.RRTypeCname,
						TTL:  aws.Int64(300),
						ResourceRecords: []r53types.ResourceRecord{
							{
								Value: aws.String(cfDomain),
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update DNS record: %w", err)
	}

	fmt.Println("  ✓ DNS record updated")
	return nil
}

func (pm *PreviewManager) getHostedZoneID(ctx context.Context) (string, error) {
	result, err := pm.r53Client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName: aws.String(pm.cfg.BaseDomain),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list hosted zones: %w", err)
	}

	if len(result.HostedZones) == 0 {
		return "", fmt.Errorf("no hosted zone found for domain: %s", pm.cfg.BaseDomain)
	}

	zoneID := *result.HostedZones[0].Id
	parts := strings.Split(zoneID, "/")
	return parts[len(parts)-1], nil
}

func (pm *PreviewManager) postGitHubComment(ctx context.Context) error {
	if pm.githubClient == nil {
		fmt.Println("Skipping GitHub comment (no GitHub token provided)")
		return nil
	}

	fmt.Println("Posting GitHub PR comment...")

	previewURL := fmt.Sprintf("https://%s", pm.fullDomain)
	commentBody := fmt.Sprintf(`## Preview Environment Deployed Successfully! 🚀

Your preview environment is now available at:
**%s**

Note: Initial deployment may take 3-5 minutes for CloudFront to propagate globally.`, previewURL)

	comment := &github.IssueComment{
		Body: github.String(commentBody),
	}

	_, _, err := pm.githubClient.Issues.CreateComment(ctx, pm.cfg.RepoOwner, pm.cfg.RepoName, pm.cfg.PRNumber, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment: %w", err)
	}

	fmt.Println("  ✓ GitHub PR comment posted")
	return nil
}
