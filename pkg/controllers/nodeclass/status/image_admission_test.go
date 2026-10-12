package status_test

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/alibabacloud-go/tea/tea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Exercise the installed CRD through a real local Kubernetes API server, without
// registering a webhook or contacting ECS. Go-only validation cannot prove this.
var _ = Describe("Image selector admission", func() {
	It("requires exact IDs for Custom image compatibility", func() {
		class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{GenerateName: "custom-policy-"}, Spec: v1alpha1.ECSNodeClassSpec{
			VSwitchSelectorTerms:       []v1alpha1.VSwitchSelectorTerm{{ID: tea.String("vsw-test")}},
			SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{{ID: tea.String("sg-test")}},
			ImageSelectorTerms:         []v1alpha1.ImageSelectorTerm{{Name: tea.String("custom-*")}},
			ImageCompatibilityPolicy:   "Custom",
			ImagePrerequisitesVerified: true,
		}}
		Expect(apierrors.IsInvalid(env.Client.Create(ctx, class))).To(BeTrue())
		class.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-custom")}}
		class.Spec.ImagePrerequisitesVerified = false
		Expect(apierrors.IsInvalid(env.Client.Create(ctx, class))).To(BeTrue())
		class.Spec.ImagePrerequisitesVerified = true
		Expect(env.Client.Create(ctx, class)).To(Succeed())
		Expect(env.Client.Delete(ctx, class)).To(Succeed())
	})
	DescribeTable("validates selectors using the deployed CRD",
		func(terms []v1alpha1.ImageSelectorTerm, valid bool) {
			class := &v1alpha1.ECSNodeClass{ObjectMeta: metav1.ObjectMeta{GenerateName: "image-admission-"}, Spec: v1alpha1.ECSNodeClassSpec{
				VSwitchSelectorTerms:       []v1alpha1.VSwitchSelectorTerm{{ID: tea.String("vsw-test")}},
				SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{{ID: tea.String("sg-test")}},
				ImageSelectorTerms:         terms,
			}}
			err := env.Client.Create(ctx, class)
			if valid {
				Expect(err).NotTo(HaveOccurred())
				Expect(env.Client.Delete(ctx, class)).To(Succeed())
			} else {
				Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected admission rejection, got %v", err)
			}
		},
		Entry("empty term", []v1alpha1.ImageSelectorTerm{{}}, false),
		Entry("empty tags", []v1alpha1.ImageSelectorTerm{{Tags: map[string]string{}}}, false),
		Entry("empty tag value", []v1alpha1.ImageSelectorTerm{{Tags: map[string]string{"env": ""}}}, false),
		Entry("empty tag key", []v1alpha1.ImageSelectorTerm{{Tags: map[string]string{"": "prod"}}}, false),
		Entry("invalid alias", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("public")}}, false),
		Entry("mixed ID and family", []v1alpha1.ImageSelectorTerm{{ID: tea.String("m-test"), ImageFamily: tea.String("app")}}, false),
		Entry("empty name", []v1alpha1.ImageSelectorTerm{{Name: tea.String("")}}, false),
		Entry("blank family", []v1alpha1.ImageSelectorTerm{{ImageFamily: tea.String(" ")}}, false),
		Entry("owner without alias", []v1alpha1.ImageSelectorTerm{{Name: tea.String("app"), ImageOwnerID: tea.String("123456")}}, false),
		Entry("owner on self", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("self"), ImageOwnerID: tea.String("123456")}}, false),
		Entry("owner on system", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("system"), ImageOwnerID: tea.String("123456")}}, false),
		Entry("owner on marketplace", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("marketplace"), ImageOwnerID: tea.String("123456")}}, false),
		Entry("owner overflow", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("others"), ImageOwnerID: tea.String("9223372036854775808")}}, false),
		Entry("owner leading zero", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("others"), ImageOwnerID: tea.String("0123456")}}, false),
		Entry("maximum owner", []v1alpha1.ImageSelectorTerm{{ImageOwnerAlias: tea.String("others"), ImageOwnerID: tea.String("9223372036854775807")}}, true),
		Entry("public VHD", []v1alpha1.ImageSelectorTerm{{ID: tea.String("aliyun_3_x64_20G_alibase_20260828.vhd")}}, true),
		Entry("public QCOW2", []v1alpha1.ImageSelectorTerm{{ID: tea.String("lifsea_3_x64_5G_alibase_20260519.qcow2")}}, true),
		Entry("shared image filters", []v1alpha1.ImageSelectorTerm{{Name: tea.String("app-*"), ImageOwnerAlias: tea.String("others"), ImageOwnerID: tea.String("123456"), Tags: map[string]string{"env": "prod"}}}, true),
	)
})
