/*
Copyright 2017 The Kubernetes Authors.

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

package nfs

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/csi-driver-nfs/pkg/lbcontroller"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/volume"
	mount "k8s.io/mount-utils"

	azcache "sigs.k8s.io/cloud-provider-azure/pkg/cache"
)

const (
	readAheadKBMountFlagRegexPattern = "^read_ahead_kb=(.+)$"
)

var (
	readAheadKBMountFlagRegex = regexp.MustCompile(readAheadKBMountFlagRegexPattern)
)

// NodeServer driver
type NodeServer struct {
	Driver  *Driver
	mounter mount.Interface
}

// NodePublishVolume mount the volume
func (ns *NodeServer) NodePublishVolume(_ context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return nil, status.Error(codes.InvalidArgument, "Volume capability missing in request")
	}
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path not provided")
	}

	lockKey := fmt.Sprintf("%s-%s", volumeID, targetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	mountOptions := volCap.GetMount().GetMountFlags()
	if req.GetReadonly() {
		mountOptions = append(mountOptions, "ro")
	}

	var server, baseDir, subDir string
	subDirReplaceMap := map[string]string{}

	mountPermissions := ns.Driver.mountPermissions
	for k, v := range req.GetVolumeContext() {
		switch strings.ToLower(k) {
		case paramServer:
			server = v
		case paramShare:
			baseDir = v
		case paramSubDir:
			subDir = v
		case pvcNamespaceKey:
			subDirReplaceMap[pvcNamespaceMetadata] = v
		case pvcNameKey:
			subDirReplaceMap[pvcNameMetadata] = v
		case pvNameKey:
			subDirReplaceMap[pvNameMetadata] = v
		case mountOptionsField:
			if v != "" {
				mountOptions = append(mountOptions, v)
			}
		case mountPermissionsField:
			if v != "" {
				var err error
				if mountPermissions, err = strconv.ParseUint(v, 8, 32); err != nil {
					return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("invalid mountPermissions %s", v))
				}
			}
		}
	}

	if server == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("%v is a required parameter", paramServer))
	}
	if baseDir == "" {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("%v is a required parameter", paramShare))
	}

	pc := req.GetPublishContext()
	ip, ok := pc[lbcontroller.NodeAnnotation]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, fmt.Sprintf("NFS server IP not found in PublishContext %v for volume %q", pc, volumeID))
	}

	klog.Infof("NodePublishVolume found IP %q from PublishContext for volume %q", ip, volumeID)
	server = ip

	source := fmt.Sprintf("%s:%s", server, baseDir)
	if subDir != "" {
		// replace pv/pvc name namespace metadata in subDir
		subDir = replaceWithMap(subDir, subDirReplaceMap)

		source = strings.TrimRight(source, "/")
		source = fmt.Sprintf("%s/%s", source, subDir)
	}

	notMnt, err := ns.mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, os.FileMode(mountPermissions)); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			notMnt = true
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	if !notMnt {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	klog.V(2).Infof("NodePublishVolume: volumeID(%v) source(%s) targetPath(%s) mountflags(%v)", volumeID, source, targetPath, mountOptions)
	// parse read ahead mount option
	filteredMountOptions := collectMountOptions(mountOptions)
	err = ns.mounter.Mount(source, targetPath, "nfs", filteredMountOptions)
	if err != nil {
		if os.IsPermission(err) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		if strings.Contains(err.Error(), "invalid argument") {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if mountPermissions > 0 {
		if err := chmodIfPermissionMismatch(targetPath, os.FileMode(mountPermissions)); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		klog.V(2).Infof("skip chmod on targetPath(%s) since mountPermissions is set as 0", targetPath)
	}

	readAheadKB, shouldUpdateReadAhead, err := extractReadAheadKBMountFlag(mountOptions)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if shouldUpdateReadAhead {
		if err := updateReadAhead(targetPath, readAheadKB); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	klog.V(2).Infof("volume(%s) mount %s on %s succeeded", volumeID, source, targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmount the volume
func (ns *NodeServer) NodeUnpublishVolume(_ context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}

	lockKey := fmt.Sprintf("%s-%s", volumeID, targetPath)
	if acquired := ns.Driver.volumeLocks.TryAcquire(lockKey); !acquired {
		return nil, status.Errorf(codes.Aborted, volumeOperationAlreadyExistsFmt, volumeID)
	}
	defer ns.Driver.volumeLocks.Release(lockKey)

	klog.V(2).Infof("NodeUnpublishVolume: unmounting volume %s on %s", volumeID, targetPath)
	var err error
	extensiveMountPointCheck := true
	forceUnmounter, ok := ns.mounter.(mount.MounterForceUnmounter)
	if ok {
		klog.V(2).Infof("force unmount %s on %s", volumeID, targetPath)
		err = mount.CleanupMountWithForce(targetPath, forceUnmounter, extensiveMountPointCheck, 30*time.Second)
	} else {
		err = mount.CleanupMountPoint(targetPath, ns.mounter, extensiveMountPointCheck)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmount target %q: %v", targetPath, err)
	}
	klog.V(2).Infof("NodeUnpublishVolume: unmount volume %s on %s successfully", volumeID, targetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetInfo return info of the node on which this plugin is running
func (ns *NodeServer) NodeGetInfo(_ context.Context, _ *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: ns.Driver.nodeID,
	}, nil
}

// NodeGetCapabilities return the capabilities of the Node plugin
func (ns *NodeServer) NodeGetCapabilities(_ context.Context, _ *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: ns.Driver.nscap,
	}, nil
}

// NodeGetVolumeStats get volume stats
func (ns *NodeServer) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty")
	}
	if len(req.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty")
	}

	// check if the volume stats is cached
	cache, err := ns.Driver.volStatsCache.Get(req.VolumeId, azcache.CacheReadTypeDefault)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if cache != nil {
		resp := cache.(csi.NodeGetVolumeStatsResponse)
		klog.V(6).Infof("NodeGetVolumeStats: volume stats for volume %s path %s is cached", req.VolumeId, req.VolumePath)
		return &resp, nil
	}

	if _, err := os.Lstat(req.VolumePath); err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat file %s: %v", req.VolumePath, err)
	}

	volumeMetrics, err := volume.NewMetricsStatFS(req.VolumePath).GetMetrics()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get metrics: %v", err)
	}

	available, ok := volumeMetrics.Available.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume available size(%v)", volumeMetrics.Available)
	}
	capacity, ok := volumeMetrics.Capacity.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume capacity size(%v)", volumeMetrics.Capacity)
	}
	used, ok := volumeMetrics.Used.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume used size(%v)", volumeMetrics.Used)
	}

	inodesFree, ok := volumeMetrics.InodesFree.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes free(%v)", volumeMetrics.InodesFree)
	}
	inodes, ok := volumeMetrics.Inodes.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes(%v)", volumeMetrics.Inodes)
	}
	inodesUsed, ok := volumeMetrics.InodesUsed.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes used(%v)", volumeMetrics.InodesUsed)
	}

	resp := csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				Total:     capacity,
				Used:      used,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: inodesFree,
				Total:     inodes,
				Used:      inodesUsed,
			},
		},
	}

	// cache the volume stats per volume
	ns.Driver.volStatsCache.Set(req.VolumeId, resp)
	return &resp, err
}

// NodeUnstageVolume unstage volume
func (ns *NodeServer) NodeUnstageVolume(_ context.Context, _ *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeStageVolume stage volume
func (ns *NodeServer) NodeStageVolume(_ context.Context, _ *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeExpandVolume node expand volume
func (ns *NodeServer) NodeExpandVolume(_ context.Context, _ *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func makeDir(pathname string) error {
	err := os.MkdirAll(pathname, os.FileMode(0755))
	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func collectMountOptions(mntFlags []string) []string {
	var options []string
	for _, opt := range mntFlags {
		if readAheadKBMountFlagRegex.FindString(opt) != "" {
			// The read_ahead_kb flag is a special flag that isn't
			// passed directly as an option to the mount command.
			continue
		}
		options = append(options, opt)
	}
	return options
}

func extractReadAheadKBMountFlag(mountFlags []string) (int64, bool, error) {
	for _, mountFlag := range mountFlags {
		if readAheadKB := readAheadKBMountFlagRegex.FindStringSubmatch(mountFlag); len(readAheadKB) == 2 {
			// There is only one matching pattern in readAheadKBMountFlagRegex
			// If found, it will be at index 1
			readAheadKBInt, err := strconv.ParseInt(readAheadKB[1], 10, 0)
			if err != nil {
				return -1, false, fmt.Errorf("invalid read_ahead_kb mount flag %q: %v", mountFlag, err)
			}
			if readAheadKBInt < 0 {
				return -1, false, fmt.Errorf("invalid negative value for read_ahead_kb mount flag: %q", mountFlag)
			}
			return readAheadKBInt, true, nil
		}
	}
	return -1, false, nil
}

func updateReadAhead(targetMountPath string, readAheadKB int64) error {
	cmd := exec.Command("mountpoint", "-d", targetMountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("Error executing mountpoint command on target path %s", targetMountPath)
		if exitError, ok := err.(*exec.ExitError); ok {
			fmt.Printf("Exit code: %d\n", exitError.ExitCode())
		}
		return status.Error(codes.Internal, err.Error())
	}

	outputStr := strings.TrimSpace(string(output))
	klog.Infof("output of mountpoint for target mount path %s: %s", targetMountPath, output)

	// Update the target value
	sysClassEchoCmd := fmt.Sprintf("echo %d > /sys/class/bdi/%s/read_ahead_kb", readAheadKB, outputStr)
	klog.V(2).Infof("Executing command %s", sysClassEchoCmd)
	cmd = exec.Command("sh", "-c", sysClassEchoCmd)
	_, err = cmd.CombinedOutput()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	// Verify updated value
	sysClassCatCmd := fmt.Sprintf("cat /sys/class/bdi/%s/read_ahead_kb", outputStr)
	klog.V(2).Infof("Executing command %s", sysClassCatCmd)
	cmd = exec.Command("sh", "-c", sysClassCatCmd)
	op, err := cmd.CombinedOutput()
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	klog.V(2).Infof("Output of %q : %s", sysClassCatCmd, op)

	opStr := strings.TrimSpace(string(op))
	updatedVal, err := strconv.ParseInt(opStr, 10, 0)
	if err != nil {
		return fmt.Errorf("invalid read_ahead_kb %v", err)
	}
	if updatedVal != readAheadKB {
		return fmt.Errorf("mismatch in read_ahead_kb, expected %d, got %d", readAheadKB, updatedVal)
	}

	return nil
}
