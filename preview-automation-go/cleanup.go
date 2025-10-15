package main

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-github/v66/github"
)

func (pm *PreviewManager) Cleanup(ctx context.Context) error {
	fmt.Println("Starting cleanup...")

	distributionID, err := pm.findCloudFrontDistribution(ctx)
	if err != nil {
		return fmt.Errorf("failed to find distribution: %w", err)
	}

	if distributionID != "" {
		if err := pm.deleteCloudFrontDistribution(ctx, distributionID); err != nil {
			return fmt.Errorf("failed to delete CloudFront distribution: %w", err)
		}
	} else {
		fmt.Println("  No CloudFront distribution found")
	}

	if err := pm.deleteRoute53Record(ctx); err != nil {
		fmt.Printf("  Warning: Failed to delete Route53 record: %v\n", err)
	}

	if err := pm.deleteS3Bucket(ctx); err != nil {
		return fmt.Errorf("failed to delete S3 bucket: %w", err)
	}

	if err := pm.postCleanupGitHubComment(ctx); err != nil {
		fmt.Printf("Warning: Failed to post GitHub comment: %v\n", err)
	}

	return nil
}

func (pm *PreviewManager) deleteCloudFrontDistribution(ctx context.Context, distributionID string) error {
	fmt.Printf("Deleting CloudFront distribution: %s\n", distributionID)

	distConfig, err := pm.cfClient.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{
		Id: aws.String(distributionID),
	})
	if err != nil {
		return fmt.Errorf("failed to get distribution config: %w", err)
	}

	if *distConfig.DistributionConfig.Enabled {
		fmt.Println("  Disabling distribution...")
		distConfig.DistributionConfig.Enabled = aws.Bool(false)

		_, err = pm.cfClient.UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
			Id:                 aws.String(distributionID),
			DistributionConfig: distConfig.DistributionConfig,
			IfMatch:            distConfig.ETag,
		})
		if err != nil {
			return fmt.Errorf("failed to disable distribution: %w", err)
		}

		fmt.Println("  Waiting for distribution to be disabled...")
		waiter := cloudfront.NewDistributionDeployedWaiter(pm.cfClient)
		err = waiter.Wait(ctx, &cloudfront.GetDistributionInput{
			Id: aws.String(distributionID),
		}, 20*time.Minute)
		if err != nil {
			return fmt.Errorf("failed waiting for distribution to be disabled: %w", err)
		}
	}

	distConfig, err = pm.cfClient.GetDistributionConfig(ctx, &cloudfront.GetDistributionConfigInput{
		Id: aws.String(distributionID),
	})
	if err != nil {
		return fmt.Errorf("failed to get updated distribution config: %w", err)
	}

	fmt.Println("  Deleting distribution...")
	_, err = pm.cfClient.DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{
		Id:      aws.String(distributionID),
		IfMatch: distConfig.ETag,
	})
	if err != nil {
		return fmt.Errorf("failed to delete distribution: %w", err)
	}

	fmt.Println("  ✓ Distribution deleted")
	return nil
}

func (pm *PreviewManager) deleteRoute53Record(ctx context.Context) error {
	fmt.Println("Deleting Route53 DNS record...")

	hostedZoneID, err := pm.getHostedZoneID(ctx)
	if err != nil {
		return err
	}

	records, err := pm.r53Client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(hostedZoneID),
		StartRecordName: aws.String(pm.fullDomain),
		StartRecordType: r53types.RRTypeCname,
		MaxItems:        aws.Int32(1),
	})
	if err != nil {
		return fmt.Errorf("failed to list records: %w", err)
	}

	if len(records.ResourceRecordSets) == 0 {
		fmt.Println("  No DNS record found")
		return nil
	}

	recordSet := records.ResourceRecordSets[0]
	if *recordSet.Name != pm.fullDomain+"." {
		fmt.Println("  No DNS record found")
		return nil
	}

	_, err = pm.r53Client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{
				{
					Action:            r53types.ChangeActionDelete,
					ResourceRecordSet: &recordSet,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	fmt.Println("  ✓ DNS record deleted")
	return nil
}

func (pm *PreviewManager) deleteS3Bucket(ctx context.Context) error {
	fmt.Printf("Deleting S3 bucket: %s\n", pm.bucketName)

	_, err := pm.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(pm.bucketName),
	})
	if err != nil {
		fmt.Println("  Bucket does not exist")
		return nil
	}

	fmt.Println("  Deleting all objects...")
	paginator := s3.NewListObjectsV2Paginator(pm.s3Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(pm.bucketName),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list objects: %w", err)
		}

		if len(page.Contents) > 0 {
			var objects []s3types.ObjectIdentifier
			for _, obj := range page.Contents {
				objects = append(objects, s3types.ObjectIdentifier{
					Key: obj.Key,
				})
			}

			_, err = pm.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(pm.bucketName),
				Delete: &s3types.Delete{
					Objects: objects,
				},
			})
			if err != nil {
				return fmt.Errorf("failed to delete objects: %w", err)
			}
		}
	}

	_, err = pm.s3Client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(pm.bucketName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete bucket: %w", err)
	}

	fmt.Println("  ✓ Bucket deleted")
	return nil
}

func (pm *PreviewManager) postCleanupGitHubComment(ctx context.Context) error {
	if pm.githubClient == nil {
		fmt.Println("Skipping GitHub comment (no GitHub token provided)")
		return nil
	}

	fmt.Println("Posting cleanup GitHub PR comment...")

	commentBody := fmt.Sprintf(`## Preview Environment Cleanup Complete 🧹

The preview environment for PR #%d has been successfully cleaned up.

All resources have been removed:
- CloudFront distribution
- Route53 DNS records
- S3 bucket and contents`, pm.cfg.PRNumber)

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
