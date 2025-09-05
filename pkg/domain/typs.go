package domain

import (
	alidns20150109 "github.com/alibabacloud-go/alidns-20150109/v4/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	credential "github.com/aliyun/credentials-go/credentials"
)

// Clienter 定义了一个接口，用于获取客户端
// 该接口可以用于获取不同的客户端实例，如阿里云、腾讯云等
type Clienter interface {
	Client() (any, error)
}

type AccessKey struct {
	AccessKeyId     string // AccessKey ID
	AccessKeySecret string // AccessKey Secret
}

func NewAccessKey(id, secret string) Clienter {
	return &AccessKey{
		AccessKeyId:     id,
		AccessKeySecret: secret,
	}
}

// NewAliyun 创建新的阿里云客户端
func (a *AccessKey) Client() (any, error) {
	// 工程代码建议使用更安全的无AK方式，凭据配置方式请参见：https://help.aliyun.com/document_detail/378661.html。
	conf := new(credential.Config).
		SetType("access_key").
		SetAccessKeyId(a.AccessKeyId).
		SetAccessKeySecret(a.AccessKeySecret)
	credential, err := credential.NewCredential(conf)
	if err != nil {
		return nil, err
	}
	config := &openapi.Config{
		Credential: credential,
	}
	// Endpoint 请参考 https://api.aliyun.com/product/Alidns
	// client = &alidns20150109.Client{}
	client, err := alidns20150109.NewClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}
