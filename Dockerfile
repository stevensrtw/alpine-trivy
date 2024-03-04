FROM alpine:latest

LABEL maintainer "https://github.com/stevensrtw"

# Install Latest version of trivy with necessary packages and dependencies
RUN apk --no-cache add gzip tar curl ca-certificates git wget bash
ENV DISTRO_PLATFORM "Linux-64bit"
ENV URL "https://github.com/aquasecurity/trivy/releases/download/v"
ENV VERSION $(curl -s "https://api.github.com/repos/aquasecurity/trivy/releases/latest" | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
# Fetch the latest Trivy version from GitHub API
RUN GET_VERSION=$(curl -s "https://api.github.com/repos/aquasecurity/trivy/releases/latest" | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/') \
	&& VERSION=$GET_VERSION \
	&& URL=${URL} \
	&& DISTRO_PLATFORM=${DISTRO_PLATFORM} \
	&& mkdir -p /tmp/trivy \
	&& cd /tmp/trivy \
	&& curl -LJO ${URL}${VERSION}/trivy_${VERSION}_${DISTRO_PLATFORM}.tar.gz \
	&& cd /tmp/trivy \
	&& ls -lart \
	&& tar -xzf *.tar.gz\
	&& mv /tmp/trivy/trivy /usr/local/bin/trivy \
	&& rm -Rf /tmp/trivy

# Cleanup
RUN apk --no-cache del curl tar gzip wget && rm -rf /tmp/*
ENTRYPOINT ["trivy"]