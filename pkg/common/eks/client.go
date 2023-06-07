package eks

import (
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/common/consts"
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/utils"
	cdshttp "github.com/capitalonline/eks-cloud-controller-manager/pkg/utils/http"
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/utils/profile"
)

type Client struct {
	utils.Client
}

func NewClient(credential *utils.Credential, region string, clientProfile *profile.ClientProfile) (client *Client, err error) {
	client = &Client{}
	client.Init(region).
		WithCredential(credential).
		WithProfile(clientProfile)
	return
}

func NewDescribeEKSNodeRequest() (request *DescribeEKSNodeRequest) {
	request = &DescribeEKSNodeRequest{
		BaseRequest: &cdshttp.BaseRequest{},
	}
	request.SetDomain(consts.ApiHost)
	request.Init().WithApiInfo(consts.ServiceEKS, consts.ApiVersion, consts.ActionDescribeEKSNode)
	return
}

func NewDescribeEKSNodeResponse() (response *DescribeEKSNodeResponse) {
	response = &DescribeEKSNodeResponse{BaseResponse: &cdshttp.BaseResponse{}}
	return
}

func (c *Client) DescribeEKSNode(request *DescribeEKSNodeRequest) (response *DescribeEKSNodeResponse, err error) {
	if request == nil {
		request = NewDescribeEKSNodeRequest()
	}
	response = NewDescribeEKSNodeResponse()
	err = c.Send(request, response)
	return
}
