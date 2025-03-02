/*
Copyright The CloudNativePG Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package volumesnapshot

import (
	"fmt"
	"strconv"

	storagesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/internal/scheme"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("getSnapshotName", func() {
	It("should return only the backup name when the role is PVCRolePgData", func() {
		name, err := getSnapshotName("backup123", utils.PVCRolePgData)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("backup123"))
	})

	It("should return only the backup name when the role is an empty string", func() {
		name, err := getSnapshotName("backup123", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("backup123"))
	})

	It("should append '-wal' to the backup name when the role is PVCRolePgWal", func() {
		name, err := getSnapshotName("backup123", utils.PVCRolePgWal)
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal("backup123-wal"))
	})

	It("should return an error for unhandled PVCRole types", func() {
		_, err := getSnapshotName("backup123", "UNKNOWN_ROLE")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal("unhandled PVCRole type: UNKNOWN_ROLE"))
	})
})

var _ = Describe("Volumesnapshot reconciler", func() {
	const (
		namespace   = "test-namespace"
		clusterName = "clusterName"
		backupName  = "theBakcup"
	)
	var (
		cluster       *apiv1.Cluster
		targetPod     *v1.Pod
		pvcs          []v1.PersistentVolumeClaim
		backup        *apiv1.Backup
		backupCounter = 1
	)

	BeforeEach(func() {
		cluster = &apiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   namespace,
				Name:        clusterName,
				Labels:      map[string]string{},
				Annotations: map[string]string{},
			},
			Spec: apiv1.ClusterSpec{
				Backup: &apiv1.BackupConfiguration{
					VolumeSnapshot: &apiv1.VolumeSnapshotConfiguration{
						ClassName: "csi-hostpath-snapclass",
					},
				},
			},
		}
		targetPod = &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      clusterName + "-2",
			},
		}
		pvcs = []v1.PersistentVolumeClaim{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-2",
					Namespace: namespace,
					Labels: map[string]string{
						utils.PvcRoleLabelName: string(utils.PVCRolePgData),
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-2-wal",
					Namespace: namespace,
					Labels: map[string]string{
						utils.PvcRoleLabelName: string(utils.PVCRolePgWal),
					},
				},
			},
		}
		backup = &apiv1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      backupName + strconv.Itoa(backupCounter),
			},
		}
		backupCounter++
	})

	It("should fence the target pod when there are no volumesnapshots", func(ctx SpecContext) {
		mockClient := fake.NewClientBuilder().
			WithScheme(scheme.BuildWithAllKnownScheme()).
			WithObjects(cluster, targetPod).
			Build()
		fakeRecorder := record.NewFakeRecorder(3)

		executor := NewExecutorBuilder(mockClient, fakeRecorder).
			FenceInstance(true).
			Build()

		result, err := executor.Execute(ctx, cluster, backup, targetPod, pvcs)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())

		var latestCluster apiv1.Cluster
		err = mockClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, &latestCluster)
		Expect(err).ToNot(HaveOccurred())

		data, err := utils.GetFencedInstances(latestCluster.Annotations)
		Expect(err).ToNot(HaveOccurred())
		Expect(data.Len()).To(Equal(1))
		Expect(data.Has(targetPod.Name)).To(BeTrue())

		var snapshotList storagesnapshotv1.VolumeSnapshotList
		err = mockClient.List(ctx, &snapshotList)
		Expect(err).ToNot(HaveOccurred())
		Expect(snapshotList.Items).NotTo(BeEmpty())
	})

	It("should not fence the target pod when there are existing volumesnapshots", func(ctx SpecContext) {
		snapshots := storagesnapshotv1.VolumeSnapshotList{
			Items: []storagesnapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      backup.Name,
						Labels: map[string]string{
							utils.BackupNameLabelName: backup.Name,
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      backup.Name + "-wal",
						Labels: map[string]string{
							utils.BackupNameLabelName: backup.Name,
						},
					},
				},
			},
		}

		mockClient := fake.NewClientBuilder().
			WithScheme(scheme.BuildWithAllKnownScheme()).
			WithObjects(cluster, targetPod).
			WithLists(&snapshots).
			Build()
		fakeRecorder := record.NewFakeRecorder(3)

		executor := NewExecutorBuilder(mockClient, fakeRecorder).
			FenceInstance(true).
			Build()

		result, err := executor.Execute(ctx, cluster, backup, targetPod, pvcs)
		Expect(err).ToNot(HaveOccurred())
		// we should have found snapshots that are not ready, and so we'd return to
		// wait for them to be ready
		Expect(result).ToNot(BeNil())

		var latestCluster apiv1.Cluster
		err = mockClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, &latestCluster)
		Expect(err).ToNot(HaveOccurred())

		data, err := utils.GetFencedInstances(latestCluster.Annotations)
		Expect(err).ToNot(HaveOccurred())
		Expect(data.Len()).To(Equal(0))
	})

	It("should unfence the target pod when the snapshots are ready", func(ctx SpecContext) {
		snapshots := storagesnapshotv1.VolumeSnapshotList{
			Items: []storagesnapshotv1.VolumeSnapshot{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      backup.Name,
						Labels: map[string]string{
							utils.BackupNameLabelName: backup.Name,
						},
						Annotations: map[string]string{
							"avoid": "nil",
						},
					},
					Status: &storagesnapshotv1.VolumeSnapshotStatus{
						ReadyToUse:   ptr.To(true),
						Error:        nil,
						CreationTime: ptr.To(metav1.Now()),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      backup.Name + "-wal",
						Labels: map[string]string{
							utils.BackupNameLabelName: backup.Name,
						},
						Annotations: map[string]string{
							"avoid": "nil",
						},
					},
					Status: &storagesnapshotv1.VolumeSnapshotStatus{
						ReadyToUse:   ptr.To(true),
						Error:        nil,
						CreationTime: ptr.To(metav1.Now()),
					},
				},
			},
		}

		cluster.Annotations[utils.FencedInstanceAnnotation] = fmt.Sprintf(`["%s"]`, targetPod.Name)

		mockClient := fake.NewClientBuilder().
			WithScheme(scheme.BuildWithAllKnownScheme()).
			WithObjects(cluster, targetPod).
			WithLists(&snapshots).
			Build()
		fakeRecorder := record.NewFakeRecorder(3)

		executor := NewExecutorBuilder(mockClient, fakeRecorder).
			FenceInstance(true).
			Build()

		result, err := executor.Execute(ctx, cluster, backup, targetPod, pvcs)
		Expect(err).ToNot(HaveOccurred())
		// we should have found snapshots that are ready, and so the result
		// should be nil
		Expect(result).To(BeNil())

		var latestCluster apiv1.Cluster
		err = mockClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, &latestCluster)
		Expect(err).ToNot(HaveOccurred())

		data, err := utils.GetFencedInstances(latestCluster.Annotations)
		Expect(err).ToNot(HaveOccurred())
		Expect(data.Len()).To(Equal(0))
	})
})
