package main

import (
	"github.com/sirupsen/logrus"
)

// When a pod is added, figure out what services it will be a member of and
// enqueue them. obj must have *v1.Pod type.
func (e *Controller) addPod(obj interface{}) {
	pod := obj.(*v1.Pod)
	services, err := e.serviceSelectorCache.GetPodServiceMemberships(e.serviceLister, pod)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Unable to get pod %s/%s's service memberships: %v", pod.Namespace, pod.Name, err))
		return
	}
	for key := range services {
		e.queue.AddAfter(key, e.endpointUpdatesBatchPeriod)
	}
}

func podToEndpointAddressForService(svc *v1.Service, pod *v1.Pod) (*v1.EndpointAddress, error) {
	var endpointIP string
	ipFamily := v1.IPv4Protocol

	if len(svc.Spec.IPFamilies) > 0 {
		// controller is connected to an api-server that correctly sets IPFamilies
		ipFamily = svc.Spec.IPFamilies[0] // this works for headful and headless
	} else {
		// controller is connected to an api server that does not correctly
		// set IPFamilies (e.g. old api-server during an upgrade)
		// TODO (khenidak): remove by when the possibility of upgrading
		// from a cluster that does not support dual stack is nil
		if len(svc.Spec.ClusterIP) > 0 && svc.Spec.ClusterIP != v1.ClusterIPNone {
			// headful service. detect via service clusterIP
			if utilnet.IsIPv6String(svc.Spec.ClusterIP) {
				ipFamily = v1.IPv6Protocol
			}
		} else {
			// Since this is a headless service we use podIP to identify the family.
			// This assumes that status.PodIP is assigned correctly (follows pod cidr and
			// pod cidr list order is same as service cidr list order). The expectation is
			// this is *most probably* the case.

			// if the family was incorrectly identified then this will be corrected once the
			// the upgrade is completed (controller connects to api-server that correctly defaults services)
			if utilnet.IsIPv6String(pod.Status.PodIP) {
				ipFamily = v1.IPv6Protocol
			}
		}
	}

	// find an ip that matches the family
	for _, podIP := range pod.Status.PodIPs {
		if (ipFamily == v1.IPv6Protocol) == utilnet.IsIPv6String(podIP.IP) {
			endpointIP = podIP.IP
			break
		}
	}

	if endpointIP == "" {
		return nil, fmt.Errorf("failed to find a matching endpoint for service %v", svc.Name)
	}

	return &v1.EndpointAddress{
		IP:       endpointIP,
		NodeName: &pod.Spec.NodeName,
		TargetRef: &v1.ObjectReference{
			Kind:      "Pod",
			Namespace: pod.ObjectMeta.Namespace,
			Name:      pod.ObjectMeta.Name,
			UID:       pod.ObjectMeta.UID,
		},
	}, nil
}

func main() {
	logrus.Info("hello")
}
