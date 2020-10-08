/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Mostly a copy of the CRD package, need to rework/extract the duplicated code for reuse between the two packages.
// Focus currently lies on having something that works first.
//
// Generates a swagger.json specification
// Target is to have the specification have
// - with full model information in the definition section of the spec based on the CRD go source code.
// - with path information
// The generated swagger.json specification is intended to be consumed by code generation tools.
package swagger
