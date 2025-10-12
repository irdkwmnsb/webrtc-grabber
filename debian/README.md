# Deb packaging

Also see https://wiki.debian.org/Packaging/Intro

1. Setup (once per distro release):

```sh
# Developer info
cat <<_EOF >>~/.bashrc
export DEBFULLNAME='Me'
export DEBEMAIL='me@example.com'
_EOF
. ~/.bashrc

# Nodesource repo for electron build
curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
    | gpg --dearmor -o ../nodesource.gpg
sudo cp ../nodesource.gpg /etc/apt/keyrings/nodesource.gpg
echo "deb [arch=amd64 signed-by=/etc/apt/trusted.gpg.d/nodesource.gpg] http://deb.nodesource.com/node_${node_version}.x nodistro main" \
    | sudo tee /etc/apt/sources.list.d/nodesource.list

# Build & packaging dependencies
sudo apt-get install pbuilder ubuntu-dev-tools debhelper apt-file nodejs

# pbuilder environment
dist=jammy
node_version=20
pbuilder-dist $dist create \
    --components 'main restricted universe' \
    --keyring ../nodesource.gpg \
    --othermirror "deb [arch=amd64 signed-by=/etc/apt/trusted.gpg.d/nodesource.gpg] http://deb.nodesource.com/node_${node_version}.x nodistro main"
```

2. Prepare source archive:

```sh
rev=HEAD
git checkout $rev
version=$(git describe --tags | sed -E -e 's/^[^0-9]+//g' -e 's/-/./g')
echo $version
# 0.1.0.beta.29.g9a60fd2, 1.0.1.alpha, ...
git archive --output ../webrtc-grabber_$version.orig.tar.gz $rev
```

3. Update version & changelog:

```sh
build=1
dch -v $version-$build
```

4. Build unreleased package:

```sh
# --use-network for npm install
pdebuild --buildresult . -- --basetgz ~/pbuilder/jammy-base.tgz --use-network yes
```

5. Install & test unreleased package

```sh
sudo apt-get install webrtc-grabber-{agent,relay}_${version}-${build}_amd64.deb
# Test here
sudo apt-get remove webrtc-grabber-{agent,relay}
sudo apt-get purge webrtc-grabber-{agent,relay}
```

6. Finalize changelog, build release package, commit changelog:

```sh
dch -r
pdebuild --buildresult . -- --basetgz ~/pbuilder/jammy-base.tgz --use-network yes
git add debian/changelog
git commit -m "debian $version-$build"
```

