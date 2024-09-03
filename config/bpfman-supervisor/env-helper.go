package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

const (
	deploymentName = "bpfman-operator"
	daemonsetName  = "bpfman-daemon"
	namespace      = "bpfman"
)

var lastDeploymentEnvContent string
var lastDaemonSetEnvContent string

func main() {
	var envDir string

	flag.StringVar(&envDir, "env-dir", "/etc/profile.d", "Base path to write environment files.")
	flag.Parse()

	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to local config.
		if home := homedir.HomeDir(); home != "" {
			configPath := filepath.Join(home, ".kube", "config")
			config, err = clientcmd.BuildConfigFromFlags("", configPath)
			if err != nil {
				fmt.Printf("Error creating local config: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Error creating in-cluster config: %v\n", err)
			os.Exit(1)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Write the kind-host-path.sh script to /etc/profile.d.
	if err := writeKindHostPathScript(envDir); err != nil {
		fmt.Printf("Error writing kind-host-path.sh: %v\n", err)
		os.Exit(1)
	}

	// Generate and write the Kubernetes environment variables once
	kubeEnvFilePath := filepath.Join(envDir, "kube.sh")
	if err := writeKubeEnvFile(kubeEnvFilePath, clientset); err != nil {
		fmt.Printf("Error writing kube.sh: %v\n", err)
		os.Exit(1)
	}

	// Create a list watcher for the deployments.
	deploymentListWatcher := cache.NewListWatchFromClient(
		clientset.AppsV1().RESTClient(),
		"deployments",
		namespace,
		fields.Everything(),
	)

	// Create a list watcher for the daemonsets.
	daemonSetListWatcher := cache.NewListWatchFromClient(
		clientset.AppsV1().RESTClient(),
		"daemonsets",
		namespace,
		fields.Everything(),
	)

	// Create an informer for deployments.
	deploymentInformer := cache.NewSharedIndexInformer(
		deploymentListWatcher,
		&v1.Deployment{},
		5*time.Second,
		cache.Indexers{},
	)

	// Create an informer for daemonsets.
	daemonSetInformer := cache.NewSharedIndexInformer(
		daemonSetListWatcher,
		&v1.DaemonSet{},
		5*time.Second,
		cache.Indexers{},
	)

	// Add event handlers to the deployment informer.
	deploymentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			deployment := obj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				writeEnvFileFromDeployment(deployment, "AddFunc", filepath.Join(envDir, deploymentName+".sh"), clientset)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			deployment := newObj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				writeEnvFileFromDeployment(deployment, "UpdateFunc", filepath.Join(envDir, deploymentName+".sh"), clientset)
			}
		},
		DeleteFunc: func(obj interface{}) {
			deployment := obj.(*v1.Deployment)
			if deployment.Name == deploymentName {
				fmt.Printf("Deployment %s/%s deleted. Event: DeleteFunc\n", namespace, deploymentName)
			}
		},
	})

	// Add event handlers to the daemonset informer.
	daemonSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			daemonSet := obj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				writeEnvFileFromDaemonSet(daemonSet, "AddFunc", filepath.Join(envDir, daemonsetName+".sh"), clientset)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			daemonSet := newObj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				writeEnvFileFromDaemonSet(daemonSet, "UpdateFunc", filepath.Join(envDir, daemonsetName+".sh"), clientset)
			}
		},
		DeleteFunc: func(obj interface{}) {
			daemonSet := obj.(*v1.DaemonSet)
			if daemonSet.Name == daemonsetName {
				fmt.Printf("DaemonSet %s/%s deleted. Event: DeleteFunc\n", namespace, daemonsetName)
			}
		},
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	go deploymentInformer.Run(stopCh)
	go daemonSetInformer.Run(stopCh)

	// Wait for signals to stop the program.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("Shutting down...")
}

func writeEnvFileFromDeployment(deployment *v1.Deployment, event, envFilePath string, clientset *kubernetes.Clientset) {
	envFileContent := extractEnvVarsFromDeployment(deployment, clientset)

	if envFileContent == lastDeploymentEnvContent {
		return
	}

	err := os.WriteFile(envFilePath, []byte(envFileContent), 0644)
	if err != nil {
		fmt.Printf("Error writing to file: %v\n", err)
		os.Exit(1)
	}

	lastDeploymentEnvContent = envFileContent
	fmt.Printf("Deployment %s/%s environment variables written to %s (%s)\n", namespace, deploymentName, envFilePath, event)
}

func writeEnvFileFromDaemonSet(daemonSet *v1.DaemonSet, event, envFilePath string, clientset *kubernetes.Clientset) {
	envFileContent := extractEnvVarsFromDaemonSet(daemonSet, clientset)

	if envFileContent == lastDaemonSetEnvContent {
		return
	}

	err := os.WriteFile(envFilePath, []byte(envFileContent), 0644)
	if err != nil {
		fmt.Printf("Error writing to file: %v\n", err)
		os.Exit(1)
	}

	lastDaemonSetEnvContent = envFileContent
	fmt.Printf("DaemonSet %s/%s environment variables written to %s (%s)\n", namespace, daemonsetName, envFilePath, event)
}

func generateKubernetesEnvVars(clientset *kubernetes.Clientset) (string, string) {
	var envVars []string

	// Retrieve the Kubernetes service from the "default" namespace.
	kubeService, err := clientset.CoreV1().Services("default").Get(context.TODO(), "kubernetes", metav1.GetOptions{})
	if err != nil {
		fmt.Printf("Error retrieving Kubernetes service: %v\n", err)
		return "", ""
	}

	// Get the ClusterIP and the port.
	kubeHost := kubeService.Spec.ClusterIP
	kubePort := kubeService.Spec.Ports[0].Port

	// Create the kubeconfig generation script content
	kubeconfigScript := fmt.Sprintf(`
#!/bin/bash

# XXX for the moment, generate every time.
rm -f /tmp/kubeconfig

# Check if kubeconfig already exists
if [ ! -f /tmp/kubeconfig ]; then
    # Use sudo to generate the kubeconfig as root
    sudo bash -c '
    mkdir -p /tmp
    cat <<EOF > /tmp/kubeconfig
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://%v:%v
    certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
  name: in-cluster
contexts:
- context:
    cluster: in-cluster
    namespace: $(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
    user: default
  name: in-cluster
current-context: in-cluster
users:
- name: default
  user:
    token: $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
EOF
    '
fi

# Mode is 0400.
export KUBECONFIG=/tmp/kubeconfig
`, kubeHost, kubePort)

	// Set the necessary KUBERNETES_* environment variables
	envVars = append(envVars,
		fmt.Sprintf("export KUBERNETES_SERVICE_HOST=%s", kubeHost),
		fmt.Sprintf("export KUBERNETES_SERVICE_PORT=%d", kubePort),
		fmt.Sprintf("export KUBERNETES_PORT_443_TCP=tcp://%s:%d", kubeHost, kubePort),
		fmt.Sprintf("export KUBERNETES_PORT_443_TCP_PROTO=tcp"),
		fmt.Sprintf("export KUBERNETES_PORT_443_TCP_ADDR=%s", kubeHost),
		fmt.Sprintf("export KUBERNETES_PORT_443_TCP_PORT=%d", kubePort),
		fmt.Sprintf("export KUBERNETES_PORT=tcp://%s:%d", kubeHost, kubePort),
		fmt.Sprintf("export KUBERNETES_SERVICE_PORT_HTTPS=%d", kubePort),
	)

	return kubeconfigScript, strings.Join(envVars, "\n") + "\n"
}

func extractEnvVarsFromDeployment(deployment *v1.Deployment, clientset *kubernetes.Clientset) string {
	var envFileContent string

	// Extract environment variables from the deployment.
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Value != "" {
				envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, env.Value)
			} else if env.ValueFrom != nil {
				value, err := resolveEnvValueFrom(env.ValueFrom, clientset, deployment.Namespace)
				if err == nil {
					envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, value)
				} else {
					fmt.Printf("Error resolving env var %s: %v\n", env.Name, err)
				}
			}
		}
	}

	return envFileContent
}

func extractEnvVarsFromDaemonSet(daemonSet *v1.DaemonSet, clientset *kubernetes.Clientset) string {
	var envFileContent string

	// Extract environment variables from the daemonset.
	for _, container := range daemonSet.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Value != "" {
				envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, env.Value)
			} else if env.ValueFrom != nil {
				value, err := resolveEnvValueFrom(env.ValueFrom, clientset, daemonSet.Namespace)
				if err == nil {
					envFileContent += fmt.Sprintf("export %s=%s\n", env.Name, value)
				} else {
					fmt.Printf("Error resolving env var %s: %v\n", env.Name, err)
				}
			}
		}
	}

	return envFileContent
}

func resolveEnvValueFrom(valueFrom *corev1.EnvVarSource, clientset *kubernetes.Clientset, namespace string) (string, error) {
	if valueFrom.ConfigMapKeyRef != nil {
		// Handle ConfigMapKeyRef
		cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.TODO(), valueFrom.ConfigMapKeyRef.Name, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get configmap %s: %v", valueFrom.ConfigMapKeyRef.Name, err)
		}
		if value, exists := cm.Data[valueFrom.ConfigMapKeyRef.Key]; exists {
			return value, nil
		} else {
			return "", fmt.Errorf("key %s not found in configmap %s", valueFrom.ConfigMapKeyRef.Key, valueFrom.ConfigMapKeyRef.Name)
		}
	}

	if valueFrom.FieldRef != nil {
		switch valueFrom.FieldRef.FieldPath {
		case "spec.nodeName":
			nodeName := os.Getenv("HOSTNAME")
			if nodeName == "" {
				return "", fmt.Errorf("HOSTNAME environment variable is not set")
			}
			return nodeName, nil
		default:
			return "", fmt.Errorf("unsupported FieldRef: %s", valueFrom.FieldRef.FieldPath)
		}
	}

	// Handle other possible EnvVarSource types (e.g.,
	// SecretKeyRef, ResourceFieldRef) here if needed.

	return "", fmt.Errorf("unsupported EnvVarSource")
}

func writeKindHostPathScript(envDir string) error {
	scriptContent := `export PATH="$PATH${KIND_HOST_PATH:+:$KIND_HOST_PATH}"`
	scriptPath := filepath.Join(envDir, "kind-host-path.sh")

	err := os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}

	fmt.Printf("Script %s written successfully\n", scriptPath)
	return nil
}

func writeKubeEnvFile(envFilePath string, clientset *kubernetes.Clientset) error {
	kubeconfigScript, envVars := generateKubernetesEnvVars(clientset)

	// Combine the kubeconfig generation script and environment variable exports
	fullScript := fmt.Sprintf("%s\n%s", kubeconfigScript, envVars)

	err := os.WriteFile(envFilePath, []byte(fullScript), 0644)
	if err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}

	fmt.Printf("Kubernetes environment variables written to %s\n", envFilePath)
	return nil
}
