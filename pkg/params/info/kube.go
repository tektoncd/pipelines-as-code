package info

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

type KubeOpts struct {
	ConfigPath string
	Context    string
	Namespace  string
}

func userHomeDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		return home
	}
	return os.Getenv("HOME")
}

func (k *KubeOpts) AddFlags(cmd *cobra.Command) {
	envkconfig := os.Getenv("KUBECONFIG")
	if envkconfig == "" {
		envkconfig = fmt.Sprintf("%s/.kube/config", userHomeDir())
	}
	cmd.PersistentFlags().StringVarP(
		&k.ConfigPath,
		"kubeconfig", "k", envkconfig,
		fmt.Sprintf("Path to the kubeconfig file to use for CLI requests (default: %s)", envkconfig),
	)

	cmd.PersistentFlags().StringVarP(
		&k.Namespace,
		"namespace", "n", "",
		"If present, the namespace scope for this CLI request",
	)
}
