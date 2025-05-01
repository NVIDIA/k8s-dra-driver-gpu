/**
# Copyright 2024 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

const (
	jsonContentType = `application/json`
)

var (
	universalDeserializer = serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
)

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type admitFunc func(*admissionv1.AdmissionRequest) ([]patchOperation, error)

// Swati: skip nvidia-dra-driver-gpu ns as well
func isKubeNamespace(ns string) bool {
	return (ns == metav1.NamespacePublic || ns == metav1.NamespaceSystem)
}

func doServeAdmitFunc(w http.ResponseWriter, r *http.Request, admit admitFunc) ([]byte, error) {
	// Request validation. Only handle POST requests with a body and json content type.
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return nil, fmt.Errorf("invalid method %s, only POST is allowed", r.Method)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, fmt.Errorf("could not read request body: %v", err)
	}

	if ct := r.Header.Get("Content-Type"); ct != jsonContentType {
		w.WriteHeader(http.StatusBadRequest)
		return nil, fmt.Errorf("unsupported content type %s, only %s is supported", ct, jsonContentType)
	}

	// Parse the AdmissionReview request.
	var admissionReviewReq admissionv1.AdmissionReview
	if _, _, err := universalDeserializer.Decode(body, nil, &admissionReviewReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, fmt.Errorf("could not deserialize AdmissionReview: %v", err)
	} else if admissionReviewReq.Request == nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, errors.New("malformed admission review: Request is nil")
	}

	// Build the response
	admissionReviewResp := admissionv1.AdmissionReview{
		TypeMeta: admissionReviewReq.TypeMeta,
		Response: &admissionv1.AdmissionResponse{
			UID: admissionReviewReq.Request.UID,
		},
	}

	// Skip k8s namespaces
	var patchOps []patchOperation
	if !isKubeNamespace(admissionReviewReq.Request.Namespace) {
		patchOps, err = admit(admissionReviewReq.Request)
	}

	if err != nil {
		admissionReviewResp.Response.Allowed = false
		admissionReviewResp.Response.Result = &metav1.Status{
			Message: err.Error(),
		}
	} else {
		patchBytes, err := json.Marshal(patchOps)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return nil, fmt.Errorf("could not marshal JSON patch: %v", err)
		}
		admissionReviewResp.Response.Allowed = true
		admissionReviewResp.Response.Patch = patchBytes

		pt := admissionv1.PatchTypeJSONPatch
		admissionReviewResp.Response.PatchType = &pt
	}

	respBytes, err := json.Marshal(admissionReviewResp)
	if err != nil {
		return nil, fmt.Errorf("could not marshal AdmissionReview response: %v", err)
	}
	return respBytes, nil
}

// serveAdmitFunc is a wrapper that handles HTTP, calls doServeAdmitFunc, and writes the result.
func serveAdmitFunc(w http.ResponseWriter, r *http.Request, admit admitFunc) {
	log.Print("Handling webhook request ...")

	respBytes, err := doServeAdmitFunc(w, r, admit)
	if err != nil {
		log.Printf("Error handling webhook request: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	log.Print("Webhook request handled successfully")
	_, writeErr := w.Write(respBytes)
	if writeErr != nil {
		log.Printf("Could not write response: %v", writeErr)
	}
}

// admitFuncHandler converts an admitFunc into an http.Handler
func admitFuncHandler(admit admitFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveAdmitFunc(w, r, admit)
	})
}
