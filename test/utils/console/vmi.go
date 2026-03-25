/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2026 Red Hat, Inc.
 *
 */

package console

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type SerialConsoleOptions struct {
	ConnectionTimeout time.Duration
}

type connectionStruct struct {
	con StreamInterface
	err error
}

func SerialConsole(name, namespace string, resource string, options *SerialConsoleOptions) (StreamInterface, error) {
	// Ensure that KUBECONFIG is set or that the kubeconfig parameter is set
	configSet := false
	if os.Getenv("KUBECONFIG") != "" {
		configSet = true
	}
	if flag.Lookup("kubeconfig") != nil {
		configSet = true
	}
	if !configSet {
		return nil,
			fmt.Errorf("kubeconfig is not set, either set KUBECONFIG environment variable or use the --kubeconfig flag")
	}
	config, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %v", err)
	}

	if options != nil && options.ConnectionTimeout != 0 {
		timeoutChan := time.Tick(options.ConnectionTimeout)
		connectionChan := make(chan connectionStruct)

		go func() {
			for {

				select {
				case <-timeoutChan:
					connectionChan <- connectionStruct{
						con: nil,
						err: fmt.Errorf("Timeout trying to connect to the virtual machine instance"),
					}
					return
				default:
				}

				con, err := AsyncSubresourceHelper(config, resource, namespace, name, "console", url.Values{})
				if err != nil {
					asyncSubresourceError, ok := err.(*AsyncSubresourceError)
					// return if response status code does not equal to 400
					if !ok || asyncSubresourceError.GetStatusCode() != http.StatusBadRequest {
						connectionChan <- connectionStruct{con: nil, err: err}
						return
					}

					time.Sleep(1 * time.Second)
					continue
				}

				connectionChan <- connectionStruct{con: con, err: nil}
				return
			}
		}()
		conStruct := <-connectionChan
		return conStruct.con, conStruct.err
	} else {
		return AsyncSubresourceHelper(config, resource, namespace, name, "console", url.Values{})
	}
}
