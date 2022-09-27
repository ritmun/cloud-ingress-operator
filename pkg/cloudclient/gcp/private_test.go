package gcp

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"
	mocks2 "github.com/openshift/cloud-ingress-operator/pkg/cloudclient/mock_cloudclient/gcp"
	"google.golang.org/api/googleapi"

	"github.com/openshift/cloud-ingress-operator/pkg/testutils"
	computev1 "google.golang.org/api/compute/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	cioerrors "github.com/openshift/cloud-ingress-operator/pkg/errors"
)

func TestGetIPAddressesFromService(t *testing.T) {
	tests := []struct {
		name         string
		svc          *corev1.Service
		expected_ips []string
		expected_err error
	}{
		{
			name: "single IP",
			svc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								IP: "127.0.0.1",
							},
						},
					},
				},
			},
			expected_ips: []string{
				"127.0.0.1",
			},
		},
		{
			name: "multiple IPs",
			svc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								IP: "127.0.0.1",
							},
							{
								IP: "10.0.0.1",
							},
						},
					},
				},
			},
			expected_ips: []string{
				"127.0.0.1",
				"10.0.0.1",
			},
		},
		{
			name: "no IPs",
			svc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			expected_ips: nil,
			expected_err: cioerrors.NewLoadBalancerNotReadyError(),
		},
	}

	for _, test := range tests {
		actual, err := getIPAddressesFromService(test.svc)

		if !reflect.DeepEqual(actual, test.expected_ips) {
			t.Errorf("%s: expected %v, got %v", test.name, actual, test.expected_ips)
		}

		actualErrorType := reflect.TypeOf(err)
		expectErrorType := reflect.TypeOf(test.expected_err)
		if actualErrorType != expectErrorType {
			t.Errorf("%s error: expected %v, got %v", test.name, actualErrorType, expectErrorType)
		}
	}
}

func TestGetClusterRegion(t *testing.T) {
	infraObj := testutils.CreateGCPInfraObject("basename", testutils.DefaultAPIEndpoint, testutils.DefaultAPIEndpoint, testutils.DefaultRegionName)
	objs := []runtime.Object{infraObj}
	mocks := testutils.NewTestMock(t, objs)

	region, err := getClusterRegion(mocks.FakeKubeClient)
	if err != nil {
		t.Fatalf("Could not get cluster region: %v", err)
	}
	if region != testutils.DefaultRegionName {
		t.Fatalf("Cluster region name mismatch. Expected %s, got %s", testutils.DefaultRegionName, region)
	}

}

func TestGCPProviderDecodeEncode(t *testing.T) {
	machine := testutils.CreateGCPMachineObj("master-0", "decode", "master", "us-east1", "us-east1-b")
	objs := []runtime.Object{&machine}
	mocks := testutils.NewTestMock(t, objs)
	machineInfo := types.NamespacedName{
		Name:      machine.GetName(),
		Namespace: machine.GetNamespace(),
	}

	err := mocks.FakeKubeClient.Get(context.TODO(), machineInfo, &machine)
	if err != nil {
		t.Fatalf("Couldn't reload machine %s: %v", machine.GetName(), err)
	}

	decodedSpec, err := getGCPDecodedProviderSpec(machine)
	if err != nil {
		t.Fatalf("Failed to decode machine %s: %v", machine.GetName(), err)
	}

	_, err = encodeProviderSpec(decodedSpec)

	if err != nil {
		t.Fatalf("Failed to encode ProviderSpec for machine %s: %v", machine.GetName(), err)
	}
}

func TestEnsureGCPForwardingRuleForExtIP(t *testing.T) {

	type args struct {
		rhapiLbIP string
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	FakeGCPCli := mocks2.NewMockGCPComputeClient(ctrl)

	tests := []struct {
		name            string
		args            args
		fwdingRuleList  computev1.ForwardingRuleList
		fwdingRuleError error
		wantErr         bool
		expectedMessage string
	}{
		{
			//Case: Happy path; FR found
			name: "method should return nil when forwarding rule exists in GCP.",
			args: args{rhapiLbIP: "matching.ip"},
			fwdingRuleList: computev1.ForwardingRuleList{
				Id:             "",
				Items:          []*computev1.ForwardingRule{{IPAddress: "matching.ip"}},
				SelfLink:       "",
				Warning:        nil,
				ServerResponse: googleapi.ServerResponse{},
			},
			fwdingRuleError: nil,
			wantErr:         false,
		},
		{
			// Case: FR not found
			name: "method should return error when rule doesn't exist in GCP.",
			args: args{rhapiLbIP: "matching.ip"},
			fwdingRuleList: computev1.ForwardingRuleList{
				Id:             "",
				Items:          []*computev1.ForwardingRule{{IPAddress: "non-matching.ip"}},
				SelfLink:       "",
				Warning:        nil,
				ServerResponse: googleapi.ServerResponse{},
			},
			fwdingRuleError: nil,
			wantErr:         true,
			expectedMessage: "Forwarding rule not found in GCP for given service IP matching.ip",
		},
		{
			// Case: GCP returns error
			name:            "method should return error when GCP returns error getting forwarding rules.",
			args:            args{rhapiLbIP: "matching.ip"},
			fwdingRuleList:  computev1.ForwardingRuleList{},
			fwdingRuleError: fmt.Errorf("dummy GCP error"),
			wantErr:         true,
			expectedMessage: "dummy GCP error",
		},
	}
	var actualMessage string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			FakeGCPCli.EXPECT().GetForwardingRuleList().Times(1).Return(&test.fwdingRuleList, test.fwdingRuleError)
			gc := &Client{
				gcpComputeClient: FakeGCPCli,
				projectID:        "dummyProject",
				region:           "dummyRegion",
				clusterName:      "dummyCluster",
				computeService:   &computev1.Service{},
			}
			err := gc.ensureGCPForwardingRuleForExtIP(test.args.rhapiLbIP)
			if ((err != nil) != test.wantErr) || (test.wantErr && (err.Error() != test.expectedMessage)) {

				if err != nil {
					actualMessage = err.Error()
				} else {
					actualMessage = ""
				}
				t.Errorf("\n Error should be thrown: %v"+
					"\n Actual error thrown: %v"+
					"\n Error message expected: %v"+
					"\n Actual error message: %v",
					test.wantErr,
					err != nil,
					test.expectedMessage,
					actualMessage)

			}
		})
	}
}