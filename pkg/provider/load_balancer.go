package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/api"
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/common/consts"
	"github.com/capitalonline/eks-cloud-controller-manager/pkg/common/lb"
	v1 "k8s.io/api/core/v1"
	"math/rand"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"strconv"
)

const (
	AnnotationLbProtocol   = "service.beta.kubernetes.io/cds-load-balancer-protocol"
	AnnotationLbType       = "service.beta.kubernetes.io/cds-load-balancer-types"
	AnnotationLbSpec       = "service.beta.kubernetes.io/cds-load-balancer-specification"
	AnnotationLbBandwidth  = "service.beta.kubernetes.io/cds-load-balancer-bandwidth"
	AnnotationLbEip        = "service.beta.kubernetes.io/cds-load-balancer-eip"
	AnnotationLbAlgorithm  = "service.beta.kubernetes.io/cds-load-balancer-algorithm"
	AnnotationLbListen     = "service.eks.listen"
	LbNetTypePublic        = "public"
	LbBillingMethodCostPay = "0" // 按需计费
	LabelNodeAz            = "node.kubernetes.io/node.az"
	LbTaskSuccess          = "success"
	LbTakError             = "error"
)

const (
	LBSpecStandard = "standard" // 标准型
	LBSpecHigh     = "high"     // 高阶型
	LBSpecSuper    = "super"    // 超强型
	LBSpecExtreme  = "extreme"  // 至强型

	LBSpecNameStandard = "标准型I"
	LBSpecNameHigh     = "高阶型I"
	LBSpecNameSuper    = "超强型I"
	LBSpecNameExtreme  = "至强型I"
)

const (
	IpTypeInternal = ""
	PlatformEks    = "eks"

	UpdateListenFull  = "full"  // 全量更新
	UpdateListenExact = "exact" // 精确更新

)

var lbSpecMap = map[string]string{
	LBSpecStandard: LBSpecNameStandard,
	LBSpecHigh:     LBSpecNameHigh,
	LBSpecSuper:    LBSpecNameSuper,
	LBSpecExtreme:  LBSpecNameExtreme,
}

type LoadBalancer struct {
	clientSet *kubernetes.Clientset
}

// GetLoadBalancer 查询lb
func (l *LoadBalancer) GetLoadBalancer(ctx context.Context, clusterName string, service *v1.Service) (status *v1.LoadBalancerStatus, exists bool, err error) {

	response, err := l.describeLbInstance(ctx, clusterName, service)
	// 接口请求返回异常
	slb := response.Data
	ingresses := make([]v1.LoadBalancerIngress, 0, len(slb.VipList))
	for i := 0; i < len(slb.VipList); i++ {
		vipInfo := slb.VipList[i]
		ingresses = append(ingresses, v1.LoadBalancerIngress{
			IP: vipInfo.Vip,
		})
	}
	return &v1.LoadBalancerStatus{
		Ingress: ingresses,
	}, false, err
}

// GetLoadBalancerName 获取lb名称
func (l *LoadBalancer) GetLoadBalancerName(ctx context.Context, clusterName string, service *v1.Service) string {
	response, err := l.describeLbInstance(ctx, clusterName, service)
	if err != nil {
		return ""
	}
	slb := response.Data
	return slb.SlbName
}

// EnsureLoadBalancer 创建lb
func (l *LoadBalancer) EnsureLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) (*v1.LoadBalancerStatus, error) {

	if service.Spec.SessionAffinity != v1.ServiceAffinityNone {
		return nil, errors.New("SessionAffinity is not supported currently, only support 'None' type")
	}

	// 先查询lb是不是已经创建过了
	request := lb.NewDescribeVpcSlbRequest()
	request.SlbName = service.Name + service.Namespace + string(service.UID)
	response, err := api.DescribeVpcSlb(request)
	if err != nil {
		return nil, err
	}
	// 接口请求返回异常
	if response.Code != consts.LbRequestSuccess {
		klog.Error(fmt.Sprintf("查询slb失败，response: %#v", response))
		return nil, errors.New(response.Message)
	}
	var slbId string
	// lb不存在
	if len(response.Data.SlbId) < 1 {
		slbId, err = l.createSlb(ctx, service, nodes)
		if err != nil {
			return nil, err
		}
	} else {
		slbId = response.Data.SlbId
	}
	fmt.Println(slbId)
	err = l.UpdateLoadBalancer(ctx, clusterName, service, nodes)
	if err != nil {
		return nil, err
	}
	// 修改监听策略
	return nil, nil
}

func (l *LoadBalancer) UpdateLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) error {
	return l.updateLbListen(ctx, service, nodes)
}

func (l *LoadBalancer) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *v1.Service) error {
	return l.clearLbListen(ctx, clusterName, service)
}

// 创建slb
func (l *LoadBalancer) createSlb(ctx context.Context, service *v1.Service, nodes []*v1.Node) (string, error) {

	if len(service.Annotations) == 0 {
		return "", errors.New("service annotations is null")
	}

	// 协议
	//proctol := service.Annotations[AnnotationLbProtocol]
	lbType, err := strconv.ParseInt(service.Annotations[AnnotationLbType], 10, 64)
	if err != nil {
		return "", err
	}
	lbSpec := service.Annotations[AnnotationLbSpec]
	//lbBandwidth := service.Annotations[AnnotationLbBandwidth]

	lbBandwidth, err := strconv.ParseInt(service.Annotations[AnnotationLbBandwidth], 10, 64)
	if err != nil {
		return "", err
	}

	lbEip, err := strconv.ParseInt(service.Annotations[AnnotationLbEip], 10, 64)
	if err != nil {
		return "", err
	}

	// 随机获取一个节点所在可用区
	nodeList, err := l.clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	if nodeList == nil || len(nodeList.Items) == 0 {
		return "", errors.New("查询节点信息异常，请稍后重试")
	}
	var azList = make([]string, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		if len(node.Labels) == 0 {
			continue
		}
		if az, ok := node.Labels[LabelNodeAz]; ok {
			azList = append(azList, az)
		}
	}
	if len(azList) == 0 {
		return "", errors.New("获取az信息失败")
	}
	randomInt := rand.Intn(len(azList))
	var azCode = azList[randomInt]
	// 查询计费方案
	schemaReq := lb.NewVpcSlbBillingSchemeRequest()
	schemaReq.AvailableZoneCode = azCode
	schemaReq.NetType = LbNetTypePublic
	schemaReq.BillingMethod = LbBillingMethodCostPay
	schemaResp, err := api.VpcSlbBillingScheme(schemaReq)
	if err != nil {
		return "", err
	}
	var billingSchemeId string

	for i := 0; i < len(schemaResp.Data); i++ {
		schema := schemaResp.Data[i]
		if schema.ConfName == lbSpecMap[lbSpec] {
			billingSchemeId = schema.BillingSchemeId
			break
		}
	}
	if billingSchemeId == "" {
		return "", errors.New("未查到相关计费信息")
	}

	// 轮训策略
	//lbAlgorithm := service.Annotations[AnnotationLbAlgorithm]
	request := lb.NewPackageCreateSlbRequest()
	// 获取一个azcode
	request.AvailableZoneCode = azCode
	// 当前仅支持4层
	request.Level = int(lbType)
	request.BandwidthInfo = lb.PackageCreateSlbBandwidthInfo{
		Name: service.Name + service.Namespace + string(service.UID),
		// 获取BillingSchemeId
		BillingSchemeId: billingSchemeId,
		Qos:             int(lbBandwidth),
		Type:            lbSpec,
		IsAutoRenewal:   false,
		IsToMonth:       false,
		Duration:        0,
		EipCount:        int(lbEip),
	}
	response, err := api.PackageCreateSlb(request)
	if err != nil || response.Code != consts.LbRequestSuccess {
		return "", errors.New(fmt.Sprintf("创建lb失败%v %v", err, response.Message))
	}
	return response.Data.SlbId, l.describeTask(response.TaskId)
}

func (l *LoadBalancer) updateLbListen(ctx context.Context, service *v1.Service, nodes []*v1.Node) error {
	//listeners := make([]lb.VpcSlbUpdateListenRequestListen, 0, len(service.Spec.Ports))
	// 查询service的Annotations是否保存有上次更改时的
	var listenInfo = make(map[string]string)
	listenAnnotation, ok := service.Annotations[AnnotationLbListen]
	if ok {
		if err := json.Unmarshal([]byte(listenAnnotation), &listenAnnotation); err != nil {
			return err
		}
	}
	lbResp, err := l.describeLbInstance(ctx, "", service)
	if err != nil {
		return err
	}

	var vip string
	for i := 0; i < len(lbResp.Data.VipList); i++ {
		if lbResp.Data.VipList[i].Vip != "" {
			vip = lbResp.Data.VipList[i].Vip
			break
		}
	}
	if vip == "" {
		return errors.New("未查询到slb的vip")
	}
	var createList = make([]lb.VpcSlbUpdateListenRequestListen, 0, len(service.Spec.Ports))
	var updateList = make([]lb.VpcSlbUpdateListenRequestListen, 0, len(service.Spec.Ports))
	for i := 0; i < len(service.Spec.Ports); i++ {
		port := service.Spec.Ports[i]
		listen := lb.VpcSlbUpdateListenRequestListen{
			ListenIp:       vip,
			ListenPort:     int(port.Port),
			ListenProtocol: string(port.Protocol),
			RsList:         nil,
		}
		rsList := make([]lb.VpcSlbUpdateListenRequestRs, 0, len(nodes))

		for j := 0; j < len(nodes); j++ {
			node := nodes[j]
			var address string
			for k := 0; k < len(node.Status.Addresses); k++ {
				addr := node.Status.Addresses[k]
				if addr.Type == IpTypeInternal {
					address = addr.Address
					break
				}
			}
			rsList = append(rsList, lb.VpcSlbUpdateListenRequestRs{
				RsLanIp: address,
				RsPort:  int(port.NodePort),
			})
		}
		listen.RsList = rsList
		if _, ok := listenInfo[string(port.NodePort)]; !ok {
			createList = append(createList, listen)
		} else {
			updateList = append(updateList, listen)
		}
	}

	if len(createList) > 0 {

		request := lb.NewVpcSlbUpdateListenRequest()
		request.ListenList = createList
		request.SlbId = lbResp.Data.SlbId
		request.Platform = PlatformEks
		request.OperatorType = UpdateListenExact
		response, err := api.VpcSlbUpdateListen(request)
		if err != nil {
			return err
		}
		return l.describeTask(response.TaskId)
		//	 TODO 精确更新
	}
	if len(updateList) > 0 {
		request := lb.NewVpcSlbUpdateListenRequest()
		request.ListenList = updateList
		request.SlbId = lbResp.Data.SlbId
		request.Platform = PlatformEks
		request.OperatorType = UpdateListenFull
		response, err := api.VpcSlbUpdateListen(request)
		if err != nil {
			return err
		}
		return l.describeTask(response.TaskId)
		//	TODO 覆盖更新
	}
	return nil
}

func (l *LoadBalancer) clearLbListen(ctx context.Context, clusterName string, service *v1.Service) error {
	request := lb.NewDescribeVpcSlbRequest()
	request.SlbName = service.Name + service.Namespace + string(service.UID)
	response, err := api.DescribeVpcSlb(request)
	if err != nil {
		return err
	}
	slb := response.Data
	clearResp, err := api.VpcSlbClearListen(slb.SlbId)
	if err != nil {
		return err
	}
	return l.describeTask(clearResp.TaskId)
}

func (l *LoadBalancer) describeLbInstance(ctx context.Context, clusterName string, service *v1.Service) (*lb.DescribeVpcSlbResponse, error) {
	request := lb.NewDescribeVpcSlbRequest()
	request.SlbName = service.Name + service.Namespace + string(service.UID)
	response, err := api.DescribeVpcSlb(request)
	if err != nil {
		return nil, err
	}
	// 接口请求返回异常
	if response.Code != consts.LbRequestSuccess {
		klog.Error(fmt.Sprintf("查询slb失败，response: %#v", response))
		return nil, errors.New(response.Message)
	}
	return response, err
}

func (l *LoadBalancer) describeTask(taskId string) error {
	for i := 0; i < 600; i++ {
		resp, err := api.DescribeTask(taskId)
		if err != nil {
			return err
		}
		switch resp.Data.TaskStatus {
		case LbTaskSuccess:
			return nil
		case LbTakError:
			return errors.New("任务失败")
		default:
		}
	}
	return errors.New("任务超时")
}
