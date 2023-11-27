%define name_agent %{name}-agent
%define prefix_agent /opt/%{name_agent}
%define name_relay %{name}-relay
%define prefix_relay /opt/%{name_relay}
%define name_turn %{name}-turn
%define prefix_turn /opt/%{name_turn}
%define video_group video

Name:           webrtc-grabber
Version:        0.1.0_beta_10_g98eb6f4
Release:        1%{?dist}
Summary:        Grabber application for stealthily peeking at other computer's screens
Group:          ContestTools
ExclusiveArch:  x86_64
License:        MIT
URL:            https://github.com/irdkwmnsb/webrtc-grabber

# Make archive with:
# git archive --prefix  webrtc-grabber-0.1.0_beta_10_g98eb6f4/ --output  webrtc-grabber-0.1.0_beta_10_g98eb6f4.tar 98eb6f468462f9fe5f1a7e3c90e724309fc30cea
Source0:        %{name}-%{version}.tar

%description
The main use case is streaming live screen video from contestants' screens on ICPC World Finals as a part of ICPC Live broadcast.

############################
## UNPACK SOURCES & BUILD ##
############################

%prep
%setup -q

%build
build_root=$(pwd)

##
## Agent
##
cd $build_root
chmod +x ./grabber_build.sh
./grabber_build.sh linux x64

##
## Relay
##
cd $build_root/packages/relay/cmd/signalling
go mod tidy
go build

##
## TURN
##
cd $build_root/packages/go-turn
go mod tidy
go build

#############
## INSTALL ##
#############

%install
rm -rf $RPM_BUILD_ROOT
build_root=$(pwd)

##
## Agent
##
# Switch to agent build directory
cd $build_root/packages/grabber/build/webrtc_grabber_linux_x64
# Remove unnecessary binary-like callable scripts
rm ./resources/app/node_modules/sha.js/bin.js
rm ./resources/app/node_modules/async/support/sync-package-managers.js
# Create homedir for agent user
mkdir -p $RPM_BUILD_ROOT%{prefix_agent}/homedir
# Remove unused files
rm ./grabber-linux.sh
# Install agent binaries
cp -ra ./* $RPM_BUILD_ROOT%{prefix_agent}
# Switch to agent tools
cd $build_root/rpm/agent
# Install agent systemd unit
mkdir -p $RPM_BUILD_ROOT/etc/systemd/system
cp -a webrtc-grabber-agent.service $RPM_BUILD_ROOT/etc/systemd/system/
# Install agent D-Bus policy
mkdir -p $RPM_BUILD_ROOT/etc/dbus-1/session.d/
cp -a webrtc-grabber-agent-dbus-policy.conf $RPM_BUILD_ROOT/etc/dbus-1/session.d/webrtc-grabber-agent.conf
# Install agent example env-configuration
mkdir -p $RPM_BUILD_ROOT/etc/default
cp -a webrtc-grabber-agent.env $RPM_BUILD_ROOT/etc/default/webrtc-grabber-agent

##
## Relay
##
# Switch to relay directory
cd $build_root/packages/relay
# Create homedir for relay user
mkdir -p $RPM_BUILD_ROOT%{prefix_relay}/homedir
# Remove unused files
rm conf/recorder.json
# Install relay binary, assets and default configuration
mkdir -p $RPM_BUILD_ROOT%{prefix_relay}
cp -ra asset conf cmd/signalling/signalling $RPM_BUILD_ROOT%{prefix_relay}
# Switch to relay tools
cd $build_root/rpm/relay
# Install relay systemd unit
mkdir -p $RPM_BUILD_ROOT/etc/systemd/system
cp -a webrtc-grabber-relay.service $RPM_BUILD_ROOT/etc/systemd/system/

##
## TURN
##
# Switch to turn directory
cd $build_root/packages/go-turn
# Create homedir for turn user
mkdir -p $RPM_BUILD_ROOT%{prefix_turn}/homedir
# Install turn binary
mkdir -p $RPM_BUILD_ROOT%{prefix_turn}
cp -ra go-turn $RPM_BUILD_ROOT%{prefix_turn}
# Switch to turn tools
cd $build_root/rpm/turn
# Install turn systemd unit
mkdir -p $RPM_BUILD_ROOT/etc/systemd/system
cp -a webrtc-grabber-turn.service $RPM_BUILD_ROOT/etc/systemd/system/
# Install turn default configuration
mkdir -p $RPM_BUILD_ROOT/etc/default
cp -a webrtc-grabber-turn.env $RPM_BUILD_ROOT/etc/default/webrtc-grabber-turn

#################
## SUBPACKAGES ##
#################

##
## Subpackage 1 - agent (packages/grabber)
##


%package agent

Summary:        The grabber part of %{name}
Group:          ContestTools

Requires: /usr/bin/setfacl
Requires: /usr/sbin/useradd /usr/bin/getent
Requires: /usr/sbin/userdel

BuildRequires: /usr/bin/npm /usr/bin/npx

%description agent
Part of %{name}.
Grabber is an electron application that is running in the background and listens for incoming calls from signaling.

%files agent
# Systemd unit
/etc/systemd/system/webrtc-grabber-agent.service
# D-Bus policy
/etc/dbus-1/session.d/webrtc-grabber-agent.conf
# Configuration
%attr(0640, root, %{name_agent}) /etc/default/webrtc-grabber-agent
# Homedir for unprivileged user
%attr(0700, %{name_agent}, root) %{prefix_agent}/homedir
# Actual code
%{prefix_agent}/grabber
# suid required for chromium sandbox
%attr(4755, root, root) %{prefix_agent}/chrome-sandbox
%{prefix_agent}/chrome_crashpad_handler
%{prefix_agent}/libEGL.so
%{prefix_agent}/libGLESv2.so
%{prefix_agent}/libffmpeg.so
%{prefix_agent}/libvk_swiftshader.so
%{prefix_agent}/libvulkan.so.1
%{prefix_agent}/v8_context_snapshot.bin
%{prefix_agent}/snapshot_blob.bin
%{prefix_agent}/locales
%{prefix_agent}/icudtl.dat
%{prefix_agent}/resources
%{prefix_agent}/resources.pak
%{prefix_agent}/chrome_100_percent.pak
%{prefix_agent}/chrome_200_percent.pak
%{prefix_agent}/version
%{prefix_agent}/vk_swiftshader_icd.json
%{prefix_agent}/LICENSE
%{prefix_agent}/LICENSES.chromium.html

%pre agent
if /usr/bin/getent passwd %{name_agent}; then
  :
else
  /usr/sbin/useradd -r -d %{prefix_agent}/homedir -s /sbin/nologin %{name_agent}
  /usr/sbin/usermod -a -G %{video_group} %{name_agent}
fi

%postun agent
if [ $1 -eq 0 ]; then
  /usr/sbin/userdel %{name_agent}
fi

##
## Subpackage 2 - relay (packages/relay)
##

%package relay

Summary:        The signaling part of %{name}
Group:          ContestTools

Requires: /usr/sbin/useradd /usr/bin/getent
Requires: /usr/sbin/userdel

BuildRequires: golang >= 1.19

%description relay
Part of %{name}.
Signaling part of the suite is a statefull express http server with two socket.io endpoints.
One for connecting the peers - /peer, and another to connect the viewers - /admin.
The admin endpoint can be protected with a token that is specified in the config file.
Signaling is also responsible in providing the peers with the peer config which contains the ICE servers to establish a connection between the peers.
The peer endpoint is used to talk to the team computers.
A peerConfig is sent to the grabber upon initial connection.
The admin endpoint is used to see all available peers and initiate the connection to the peers.

%files relay
# Systemd unit
/etc/systemd/system/webrtc-grabber-relay.service
# Homedir for unprivileged user
%attr(0700, %{name_relay}, root) %{prefix_relay}/homedir
# The binary
%attr(0755, root, root) %{prefix_relay}/signalling
# Assets
%{prefix_relay}/asset
# Configuration
%attr(0640, root, %{name_relay}) %{prefix_relay}/conf/config.json

%pre relay
if /usr/bin/getent passwd %{name_relay}; then
  :
else
  /usr/sbin/useradd -r -d %{prefix_relay}/homedir -s /sbin/nologin %{name_relay}
fi

%postun relay
if [ $1 -eq 0 ]; then
  /usr/sbin/userdel %{name_relay}
fi

##
## Subpackage 3 - turn (packages/go-turn)
##

%package turn

Summary:        The TURN part of %{name}
Group:          ContestTools

Requires: /usr/sbin/useradd /usr/bin/getent
Requires: /usr/sbin/userdel

BuildRequires: golang >= 1.19

%description turn
Part of %{name}.
A pion-based TURN server.
Used to transmit video/audio data across different networks.

%files turn
# Systemd unit
/etc/systemd/system/webrtc-grabber-turn.service
# Homedir for unprivileged user
%attr(0700, %{name_turn}, root) %{prefix_turn}/homedir
# Configuration
%attr(0640, root, %{name_turn}) /etc/default/webrtc-grabber-turn
# The binary
%attr(0755, root, root) %{prefix_turn}/go-turn

%pre turn
if /usr/bin/getent passwd %{name_turn}; then
  :
else
  /usr/sbin/useradd -r -d %{prefix_turn}/homedir -s /sbin/nologin %{name_turn}
fi

%postun turn
if [ $1 -eq 0 ]; then
  /usr/sbin/userdel %{name_turn}
fi

%changelog
* Mon Nov 27 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Move tools into repository
- Use git revision v0.1.0-beta-10-g98eb6f4
* Mon Nov 27 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Agent: systemd unit: require network-online.target (instead of network.target)
- Agent: systemd unit: add TimeoutStopSec
- Agent: disable audio capture by default
- Agent: add user to video group
- Agent, Relay, Turn: handle package upgrades (postun section)
- Relay, Turn: systemd unit: add WantedBy section
- Uncomment BuildRequires back
- Used at moscow.nerc.icpc.global
* Sun Nov 19 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Agent: fix systemd unit for autostart
- Agent: update default config for ubuntu 20 target
- Don't install unused files
* Sun Nov 12 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Agent: change default dbus socket path
- Relay: fix systemd unit ExecStart
* Sun Nov 12 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Agent: better env-config permissions
- Agent: don't install grabber-linux.sh
- Agent: fix dbus policy
- Add "turn" subpackage
* Sat Nov 11 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Tools: agent: handle D-Bus session access (install a policy & set unix socket permissions)
- Rename webrtc-grabber-signaling.service to webrtc-grabber-relay (also leave an old alias)
- Rename webrtc-grabber.service to webrtc-grabber-agent (also leave an old alias)
- Add "agent" and "relay" subpackages
- Build from source
- Set license from git
- Use "git describe --tags" for version
- Git 28231a9 (v0.1.0-beta +2)
* Mon Apr 24 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- Fix build on centos7 (wip)
* Sun Apr 23 2023 Dmitrii Kuptsov <demulkup@ya.ru>
- First packaging
