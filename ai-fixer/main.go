package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// watch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	// Accept optional kubeconfig flag
	kubeconfig := flag.String("kubeconfig", "", "Path to the kubeconfig file (optional if in-cluster)")
	namespace := flag.String("namespace", "app-namespace", "Namespace to watch (default is all)")
	flag.Parse()

	// Set up config
	config, err := getKubeConfig(*kubeconfig)
	if err != nil {
		panic(fmt.Errorf("failed to get kube config: %w", err))
	}

	// Create client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(fmt.Errorf("failed to create client: %w", err))
	}

	// Setup signal handling to gracefully stop
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, os.Interrupt, syscall.SIGTERM)

	// Context for the watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-stopCh
		fmt.Println("Stopping...")
		cancel()
	}()

	// Start watching events
	fmt.Println("Starting event watcher...")
	watchEvents(ctx, clientset, *namespace)
}

func getKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	// Fallback to in-cluster config
	return rest.InClusterConfig()
}

func watchEvents(ctx context.Context, clientset *kubernetes.Clientset, namespace string) {
	watcher, err := clientset.CoreV1().Events(namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to start watch: %w", err))
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Context cancelled, exiting watch loop.")
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				fmt.Println("Watch channel closed, reconnecting...")
				time.Sleep(2 * time.Second)
				watchEvents(ctx, clientset, namespace) // Recurse for reconnect
				return
			}

			if e, ok := event.Object.(*v1.Event); ok {
				fmt.Printf("[%s] %s/%s: %s - %s\n",
					e.LastTimestamp.Format(time.RFC3339),
					e.Namespace, e.InvolvedObject.Name,
					e.Reason, e.Message)
			}
		}
	}
}
