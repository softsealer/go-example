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

// Duration is a time.Duration that uses the xsd:duration format for text
// marshalling and unmarshalling.
type Duration time.Duration

// MarshalText implements the encoding.TextMarshaler interface.
func (d Duration) MarshalText() ([]byte, error) {
	if d == 0 {
		return nil, nil
	}

	out := "PT"
	if d < 0 {
		d *= -1
		out = "-" + out
	}

	h := time.Duration(d) / time.Hour
	m := time.Duration(d) % time.Hour / time.Minute
	s := time.Duration(d) % time.Minute / time.Second
	ns := time.Duration(d) % time.Second
	if h > 0 {
		out += fmt.Sprintf("%dH", h)
	}
	if m > 0 {
		out += fmt.Sprintf("%dM", m)
	}
	if s > 0 || ns > 0 {
		out += fmt.Sprintf("%d", s)
		if ns > 0 {
			out += strings.TrimRight(fmt.Sprintf(".%09d", ns), "0")
		}
		out += "S"
	}

	return []byte(out), nil
}

const (
	day   = 24 * time.Hour
	month = 30 * day  // Assumed to be 30 days.
	year  = 365 * day // Assumed to be non-leap year.
)

var (
	durationRegexp     = regexp.MustCompile(`^(-?)P(?:(\d+)Y)?(?:(\d+)M)?(?:(\d+)D)?(?:T(.+))?$`)
	durationTimeRegexp = regexp.MustCompile(`^(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$`)
)

// UnmarshalText implements the encoding.TextUnmarshaler interface.
func (d *Duration) UnmarshalText(text []byte) error {
	if text == nil {
		*d = 0
		return nil
	}

	var (
		out  time.Duration
		sign time.Duration = 1
	)
	match := durationRegexp.FindStringSubmatch(string(text))
	if match == nil || strings.Join(match[2:6], "") == "" {
		return fmt.Errorf("invalid duration (%s)", text)
	}
	if match[1] == "-" {
		sign = -1
	}
	if match[2] != "" {
		y, err := strconv.Atoi(match[2])
		if err != nil {
			return fmt.Errorf("invalid duration years (%s): %s", text, err)
		}
		out += time.Duration(y) * year
	}
	if match[3] != "" {
		m, err := strconv.Atoi(match[3])
		if err != nil {
			return fmt.Errorf("invalid duration months (%s): %s", text, err)
		}
		out += time.Duration(m) * month
	}
	if match[4] != "" {
		d, err := strconv.Atoi(match[4])
		if err != nil {
			return fmt.Errorf("invalid duration days (%s): %s", text, err)
		}
		out += time.Duration(d) * day
	}
	if match[5] != "" {
		match := durationTimeRegexp.FindStringSubmatch(match[5])
		if match == nil {
			return fmt.Errorf("invalid duration (%s)", text)
		}
		if match[1] != "" {
			h, err := strconv.Atoi(match[1])
			if err != nil {
				return fmt.Errorf("invalid duration hours (%s): %s", text, err)
			}
			out += time.Duration(h) * time.Hour
		}
		if match[2] != "" {
			m, err := strconv.Atoi(match[2])
			if err != nil {
				return fmt.Errorf("invalid duration minutes (%s): %s", text, err)
			}
			out += time.Duration(m) * time.Minute
		}
		if match[3] != "" {
			s, err := strconv.ParseFloat(match[3], 64)
			if err != nil {
				return fmt.Errorf("invalid duration seconds (%s): %s", text, err)
			}
			out += time.Duration(s * float64(time.Second))
		}
	}

	*d = Duration(sign * out)
	return nil
}


func main() {
	logrus.Info("hello")
}
