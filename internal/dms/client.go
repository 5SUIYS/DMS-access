package dms

import (
	"context"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"
)

// DMSClient 定义与 AWS DMS 交互的接口（便于 mock 测试）。
type DMSClient interface {
	// CreateReplicationTask 创建复制任务。
	CreateReplicationTask(ctx context.Context, input *databasemigrationservice.CreateReplicationTaskInput) (*databasemigrationservice.CreateReplicationTaskOutput, error)
	// StartReplicationTask 启动复制任务。
	StartReplicationTask(ctx context.Context, input *databasemigrationservice.StartReplicationTaskInput) (*databasemigrationservice.StartReplicationTaskOutput, error)
	// DescribeReplicationTasks 查询复制任务状态。
	DescribeReplicationTasks(ctx context.Context, input *databasemigrationservice.DescribeReplicationTasksInput) (*databasemigrationservice.DescribeReplicationTasksOutput, error)
}

// awsDMSClient 是基于 aws-sdk-go-v2 的生产 DMS 客户端。
type awsDMSClient struct {
	svc *databasemigrationservice.Client
}

// NewAWSDMSClient 创建生产 DMS 客户端。
func NewAWSDMSClient(ctx context.Context, region string) (DMSClient, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return &awsDMSClient{svc: databasemigrationservice.NewFromConfig(cfg)}, nil
}

func (c *awsDMSClient) CreateReplicationTask(ctx context.Context, input *databasemigrationservice.CreateReplicationTaskInput) (*databasemigrationservice.CreateReplicationTaskOutput, error) {
	return c.svc.CreateReplicationTask(ctx, input)
}

func (c *awsDMSClient) StartReplicationTask(ctx context.Context, input *databasemigrationservice.StartReplicationTaskInput) (*databasemigrationservice.StartReplicationTaskOutput, error) {
	return c.svc.StartReplicationTask(ctx, input)
}

func (c *awsDMSClient) DescribeReplicationTasks(ctx context.Context, input *databasemigrationservice.DescribeReplicationTasksInput) (*databasemigrationservice.DescribeReplicationTasksOutput, error) {
	return c.svc.DescribeReplicationTasks(ctx, input)
}

// MockDMSClient 是用于测试的 DMS mock 客户端。
type MockDMSClient struct {
	// CreateReplicationTaskFn 可配置 CreateReplicationTask 的行为。
	CreateReplicationTaskFn func(ctx context.Context, input *databasemigrationservice.CreateReplicationTaskInput) (*databasemigrationservice.CreateReplicationTaskOutput, error)
	// StartReplicationTaskFn 可配置 StartReplicationTask 的行为。
	StartReplicationTaskFn func(ctx context.Context, input *databasemigrationservice.StartReplicationTaskInput) (*databasemigrationservice.StartReplicationTaskOutput, error)
	// DescribeReplicationTasksFn 可配置 DescribeReplicationTasks 的行为。
	DescribeReplicationTasksFn func(ctx context.Context, input *databasemigrationservice.DescribeReplicationTasksInput) (*databasemigrationservice.DescribeReplicationTasksOutput, error)

	// 调用记录
	CreateCalls   []*databasemigrationservice.CreateReplicationTaskInput
	StartCalls    []*databasemigrationservice.StartReplicationTaskInput
	DescribeCalls []*databasemigrationservice.DescribeReplicationTasksInput
}

func (m *MockDMSClient) CreateReplicationTask(ctx context.Context, input *databasemigrationservice.CreateReplicationTaskInput) (*databasemigrationservice.CreateReplicationTaskOutput, error) {
	m.CreateCalls = append(m.CreateCalls, input)
	if m.CreateReplicationTaskFn != nil {
		return m.CreateReplicationTaskFn(ctx, input)
	}
	taskARN := "arn:aws:dms:us-east-1:123456789:task:mock-task-arn"
	return &databasemigrationservice.CreateReplicationTaskOutput{
		ReplicationTask: &dmstypes.ReplicationTask{
			ReplicationTaskArn: &taskARN,
		},
	}, nil
}

func (m *MockDMSClient) StartReplicationTask(ctx context.Context, input *databasemigrationservice.StartReplicationTaskInput) (*databasemigrationservice.StartReplicationTaskOutput, error) {
	m.StartCalls = append(m.StartCalls, input)
	if m.StartReplicationTaskFn != nil {
		return m.StartReplicationTaskFn(ctx, input)
	}
	status := "starting"
	arn := ""
	if input.ReplicationTaskArn != nil {
		arn = *input.ReplicationTaskArn
	}
	return &databasemigrationservice.StartReplicationTaskOutput{
		ReplicationTask: &dmstypes.ReplicationTask{
			ReplicationTaskArn: &arn,
			Status:             &status,
		},
	}, nil
}

func (m *MockDMSClient) DescribeReplicationTasks(ctx context.Context, input *databasemigrationservice.DescribeReplicationTasksInput) (*databasemigrationservice.DescribeReplicationTasksOutput, error) {
	m.DescribeCalls = append(m.DescribeCalls, input)
	if m.DescribeReplicationTasksFn != nil {
		return m.DescribeReplicationTasksFn(ctx, input)
	}
	status := "running"
	return &databasemigrationservice.DescribeReplicationTasksOutput{
		ReplicationTasks: []dmstypes.ReplicationTask{
			{Status: &status},
		},
	}, nil
}
