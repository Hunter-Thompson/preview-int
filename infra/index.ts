import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";

// Get configuration
const config = new pulumi.Config();
const awsRegion = config.get("awsRegion") || "us-east-1";
const baseDomain = config.require("baseDomain");
const githubOrg = config.require("githubOrg");
const githubRepo = config.require("githubRepo");
const hostedZoneId = config.require("hostedZoneId");

// Create AWS providers
const defaultProvider = new aws.Provider("default", {
    region: awsRegion,
});

const usEast1Provider = new aws.Provider("us-east-1", {
    region: "us-east-1", // Required for ACM certificates for CloudFront
});


// ACM Certificate for *.preview.yourapp.com
const certificate = new aws.acm.Certificate("preview", {
    domainName: `*.${baseDomain}`,
    validationMethod: "DNS",
    subjectAlternativeNames: [baseDomain],
    tags: {
        Name: "Preview Environments",
        Environment: "preview",
    },
}, { provider: usEast1Provider });

// DNS validation for ACM certificate
const certValidationRecords = certificate.domainValidationOptions.apply(options => {
    const records: aws.route53.Record[] = [];
    const recordMap = new Map<string, any>();

    options.forEach((option, index) => {
        const domainName = option.domainName;
        if (!recordMap.has(domainName)) {
            recordMap.set(domainName, option);
            const record = new aws.route53.Record(`cert-validation-${index}`, {
                name: option.resourceRecordName,
                type: option.resourceRecordType,
                zoneId: hostedZoneId,
                records: [option.resourceRecordValue],
                ttl: 60,
                allowOverwrite: true,
            }, { provider: defaultProvider });
            records.push(record);
        }
    });

    return records;
});

// Certificate validation
const certificateValidation = new aws.acm.CertificateValidation("preview", {
    certificateArn: certificate.arn,
    validationRecordFqdns: certValidationRecords.apply(records =>
        records.map(record => record.fqdn)
    ),
}, { provider: usEast1Provider });

// IAM OpenID Connect Provider for GitHub Actions
const githubOidcProvider = new aws.iam.OpenIdConnectProvider("github", {
    url: "https://token.actions.githubusercontent.com",
    clientIdLists: ["sts.amazonaws.com"],
    thumbprintLists: [
        "6938fd4d98bab03faadb97b34396831e3780aea1",
        "1c58a3a8518e8759bf075b76b750d4f2df264fcd",
    ],
    tags: {
        Name: "GitHub Actions OIDC",
    },
}, { provider: defaultProvider });

// IAM Role for GitHub Actions
const githubActionsRole = new aws.iam.Role("github-actions", {
    name: "GithubActionsPreviewRole",
    assumeRolePolicy: pulumi.all([githubOidcProvider.arn]).apply(([oidcArn]) =>
        JSON.stringify({
            Version: "2012-10-17",
            Statement: [
                {
                    Effect: "Allow",
                    Principal: {
                        Federated: oidcArn,
                    },
                    Action: "sts:AssumeRoleWithWebIdentity",
                    Condition: {
                        StringEquals: {
                            "token.actions.githubusercontent.com:aud": "sts.amazonaws.com",
                        },
                        StringLike: {
                            "token.actions.githubusercontent.com:sub": `repo:${githubOrg}/${githubRepo}:*`,
                        },
                    },
                },
            ],
        })
    ),
    tags: {
        Name: "GitHub Actions Preview Role",
    },
}, { provider: defaultProvider });

// IAM Policy for GitHub Actions
const githubActionsPolicy = new aws.iam.RolePolicy("github-actions-policy", {
    name: "GithubActionsPreviewPolicy",
    role: githubActionsRole.id,
    policy: JSON.stringify({
        Version: "2012-10-17",
        Statement: [
            {
                Effect: "Allow",
                Action: [
                    "s3:CreateBucket",
                    "s3:DeleteBucket",
                    "s3:HeadBucket",
                    "s3:ListBucket",
                    "s3:PutObject",
                    "s3:GetObject",
                    "s3:DeleteObject",
                    "s3:PutBucketWebsite",
                    "s3:PutBucketPolicy",
                    "s3:DeleteBucketPolicy",
                    "s3:GetBucketPolicy",
                    "s3:PutBucketPublicAccessBlock",
                    "s3:GetBucketLocation",
                ],
                Resource: [
                    "arn:aws:s3:::pr-*",
                    "arn:aws:s3:::pr-*/*",
                ],
            },
            {
                Effect: "Allow",
                Action: ["s3:ListAllMyBuckets"],
                Resource: "*",
            },
            {
                Effect: "Allow",
                Action: [
                    "cloudfront:CreateDistribution",
                    "cloudfront:GetDistribution",
                    "cloudfront:GetDistributionConfig",
                    "cloudfront:UpdateDistribution",
                    "cloudfront:DeleteDistribution",
                    "cloudfront:ListDistributions",
                    "cloudfront:CreateInvalidation",
                    "cloudfront:GetInvalidation",
                    "cloudfront:TagResource",
                    "cloudfront:UntagResource",
                    "cloudfront:ListOriginAccessControls",
                    "cloudfront:CreateOriginAccessControl",
                    "cloudfront:GetOriginAccessControl",
                    "cloudfront:UpdateOriginAccessControl",
                    "cloudfront:DeleteOriginAccessControl",
                ],
                Resource: "*",
            },
            {
                Effect: "Allow",
                Action: [
                    "route53:ListHostedZones",
                    "route53:ListHostedZonesByName",
                    "route53:GetHostedZone",
                    "route53:ListResourceRecordSets",
                    "route53:ChangeResourceRecordSets",
                    "route53:GetChange",
                ],
                Resource: "*",
            },
            {
                Effect: "Allow",
                Action: [
                    "acm:DescribeCertificate",
                    "acm:ListCertificates",
                ],
                Resource: "*",
            },
        ],
    }),
}, { provider: defaultProvider });

// Exports
export const githubActionsRoleArn = githubActionsRole.arn;
export const certificateArn = certificate.arn;
export const baseDomainOutput = baseDomain;
