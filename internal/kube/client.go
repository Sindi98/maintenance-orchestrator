// Package kube provides thin, maintenance-specific helpers on top of the
// controller-runtime client: node cordon/uncordon, pod listing and
// classification, policy/v1 eviction, PDB lookup and a heuristic capacity check.
//
// All cluster I/O the rest of the controller performs goes through this package,
// keeping the domain packages (preflight, planner, executor, policy) free of raw
// client calls and therefore easy to reason about.
package kube

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelZone is the well-known topology zone label.
	LabelZone = "topology.kubernetes.io/zone"

	// annotationMirrorPod marks a pod as the API mirror of a static pod.
	annotationMirrorPod = "kubernetes.io/config.mirror"

	// OpenShift Machine Config Operator annotations used to detect nodes that
	// the MCO is currently reconfiguring (so we leave them alone).
	annotationMCOState   = "machineconfiguration.openshift.io/state"
	annotationMCOCurrent = "machineconfiguration.openshift.io/currentConfig"
	annotationMCODesired = "machineconfiguration.openshift.io/desiredConfig"

	// IndexPodNodeName is the field-index key the controller registers so that
	// pods can be listed by their assigned node from the cache.
	IndexPodNodeName = "spec.nodeName"
)

// Client wraps a controller-runtime client.Client with helper methods. The
// embedded client provides Get/List/Patch/SubResource directly.
type Client struct {
	client.Client

	// Reader is an uncached reader used for optional resources whose CRDs may be
	// absent (Machines), so listing them never starts a cache informer for a
	// non-existent kind. When nil the embedded client is used.
	Reader client.Reader
}

// New returns a Client wrapping the given controller-runtime client. Reader
// defaults to the same client; callers may override it with an APIReader.
func New(c client.Client) *Client {
	return &Client{Client: c, Reader: c}
}

// reader returns the uncached Reader, falling back to the embedded client.
func (c *Client) reader() client.Reader {
	if c.Reader != nil {
		return c.Reader
	}
	return c.Client
}
