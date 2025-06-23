// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const DefaultConfigPath string = "/etc/metal-token-rotate/config.json"

type Config struct {
	Clusters []ClusterConfig `json:"items"`
}

type ClusterConfig struct {
	ServiceAccountName      string `json:"serviceAccountName"`
	ServiceAccountNamespace string `json:"serviceAccountNamespace"`
	ExpirationSeconds       int64  `json:"expirationSeconds"`
	Identity                string `json:"identity"`
	TargetSecretName        string `json:"targetSecretName"`
	TargetSecretNamespace   string `json:"targetSecretNamespace"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	if len(config.Clusters) == 0 {
		return Config{}, errors.New("no clusters found in config")
	}
	for i, cluster := range config.Clusters {
		if err := validateCluster(&cluster); err != nil {
			return Config{}, fmt.Errorf("invalid cluster at index %d: %w", i, err)
		}
	}
	return config, nil
}

func validateCluster(cluster *ClusterConfig) error {
	if cluster.ServiceAccountName == "" {
		return errors.New("serviceAccountName is required")
	}
	if cluster.ServiceAccountNamespace == "" {
		return errors.New("serviceAccountNamespace is required")
	}
	if cluster.ExpirationSeconds <= 0 {
		cluster.ExpirationSeconds = 3600
	}
	if cluster.Identity == "" {
		return errors.New("identity is required")
	}
	if (cluster.TargetSecretName == "") != (cluster.TargetSecretNamespace == "") {
		return errors.New("both TargetSecretName and TargetSecretNamespace must be set or unset together")
	}
	return nil
}
