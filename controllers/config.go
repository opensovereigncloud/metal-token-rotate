// Copyright 2024 SAP SE
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const DefaultConfigPath string = "/etc/metal-token-rotate/config.json"

type Config struct {
	ServiceAccountName      string `json:"serviceAccountName"`
	ServiceAccountNamespace string `json:"serviceAccountNamespace"`
	ExpirationSeconds       int64  `json:"expirationSeconds"`
	Identity                string `json:"identity"`
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
	if config.ServiceAccountName == "" {
		return Config{}, errors.New("serviceAccountName is required")
	}
	if config.ServiceAccountNamespace == "" {
		return Config{}, errors.New("serviceAccountNamespace is required")
	}
	if config.ExpirationSeconds == 0 {
		config.ExpirationSeconds = 3600
	}
	if config.Identity == "" {
		return Config{}, errors.New("identity is required")
	}
	return config, nil
}
