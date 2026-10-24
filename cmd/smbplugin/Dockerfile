# Copyright 2019 The Kubernetes Authors.
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

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:754d5200fc3edae76d3b6598a9a3fac7e74981ee6a5413ab955025f185753ef2 AS trust-check
COPY hack/verify-runtime-trust.go /src/verify-runtime-trust.go
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /verify-runtime-trust /src/verify-runtime-trust.go

FROM registry.k8s.io/build-image/debian-base:bookworm-v1.0.8

RUN apt update && apt upgrade -y && apt-mark unhold libcap2 && clean-install ca-certificates cifs-utils util-linux mount udev

COPY --from=trust-check /verify-runtime-trust /tmp/verify-runtime-trust
RUN /tmp/verify-runtime-trust /etc/ssl/certs/ca-certificates.crt && rm /tmp/verify-runtime-trust

LABEL maintainers="andyzhangx"
LABEL description="SMB CSI Driver"
ARG ARCH=amd64
ARG binary=./_output/${ARCH}/smbplugin
COPY ${binary} /smbplugin

RUN echo "[libdefaults]\n    default_ccache_name = FILE:/var/lib/kubelet/kerberos/krb5cc_%{uid}\n" > /etc/krb5.conf

ENTRYPOINT ["/smbplugin"]
