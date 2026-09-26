# Linux KVM / Windows 10 / MuMu setup record

**Status: blocked at Windows-installation media; rootless libvirt is usable.** This verification used `qemu:///session` only. It did not use `sudo`, `pkexec`, credentials, or system-service changes, and it did not access or modify `qemu:///system`. No VM, qcow2 disk, NVRAM file, ISO, Windows guest, MuMu installer, game, APK, ROM, video, or application source was created, downloaded, changed, or deleted.

Evidence was collected on `2026-09-26T02:43:55+08:00` unless a command output below gives a more specific time.

## Rootless connection evidence

The user-session libvirt daemon/socket is available at `XDG_RUNTIME_DIR=/run/user/1000`; the process is user `x` (`uid=1000`, `gid=1000`). Relevant runtime paths existed:

```text
$ find "$XDG_RUNTIME_DIR/libvirt" -maxdepth 4 -printf '%M %u:%g %p\n'
drwx------ x:x /run/user/1000/libvirt
drwxrwxr-x x:x /run/user/1000/libvirt/qemu
drwxrwxr-x x:x /run/user/1000/libvirt/qemu/run
-rw------- x:x /run/user/1000/libvirt/qemu/run/autostarted
-rw-r--r-- x:x /run/user/1000/libvirt/qemu/run/driver.pid
drwxrwx--- x:x /run/user/1000/libvirt/qemu/run/dbus
drwxrwxr-x x:x /run/user/1000/libvirt/qemu/run/passt
drwxrwxr-x x:x /run/user/1000/libvirt/qemu/run/slirp
drwxrwxr-x x:x /run/user/1000/libvirt/qemu/run/channel
drwxrwxr-x x:x /run/user/1000/libvirt/storage
drwxrwxr-x x:x /run/user/1000/libvirt/storage/run
-rw------- x:x /run/user/1000/libvirt/storage/run/autostarted
-rw-r--r-- x:x /run/user/1000/libvirt/storage/run/driver.pid
srwx------ x:x /run/user/1000/libvirt/libvirt-admin-sock
srwx------ x:x /run/user/1000/libvirt/libvirt-sock
-rw-r--r-- x:x /run/user/1000/libvirt/libvirtd.pid
```

The connection is live and is using QEMU 8.2.2 through libvirt 10.0.0:

```text
$ virsh -c qemu:///session uri
qemu:///session
exit=0

$ virsh -c qemu:///session version
Compiled against library: libvirt 10.0.0
Using library: libvirt 10.0.0
Using API: QEMU 10.0.0
Running hypervisor: QEMU 8.2.2
exit=0
```

KVM access and all required basic virtualization checks pass. `virt-host-validate` returned success with non-fatal host warnings:

```text
$ virt-host-validate qemu
QEMU: Checking for hardware virtualization                 : PASS
QEMU: Checking if device /dev/kvm exists                   : PASS
QEMU: Checking if device /dev/kvm is accessible            : PASS
QEMU: Checking if device /dev/cpu/0/msr exists             : PASS
QEMU: Checking if device /dev/vhost-net exists             : PASS
QEMU: Checking if device /dev/net/tun exists               : PASS
QEMU: Checking for cgroup 'cpu' controller support         : PASS
QEMU: Checking for cgroup 'cpuacct' controller support     : PASS
QEMU: Checking for cgroup 'cpuset' controller support      : PASS
QEMU: Checking for cgroup 'memory' controller support      : PASS
QEMU: Checking for cgroup 'devices' controller support     : WARN (Enable 'devices' in kernel Kconfig file or mount/enable cgroup controller in your system)
QEMU: Checking for cgroup 'blkio' controller support       : PASS
QEMU: Checking for device assignment IOMMU support         : WARN (No ACPI DMAR table found, IOMMU either disabled in BIOS or not supported by this hardware platform)
QEMU: Checking for secure guest support                    : WARN (Unknown if this platform has Secure Guest support)
exit=0
```

The warnings do not block this non-device-passthrough VM. `/dev/kvm` accessibility passed. Session capabilities report `arch=x86_64` and a host CPU feature named `vmx`; therefore VMX exposure can be requested with a host-passthrough CPU when provisioning is resumed. No claim is made yet that a nested hypervisor has run inside a guest.

## Session inventory and preservation

The session has no domains, pools, or libvirt virtual networks. These are current read-only inventory results:

```text
$ virsh -c qemu:///session list --all
 Id   Name   State
--------------------
exit=0

$ virsh -c qemu:///session pool-list --all --details
 Name   State   Autostart   Persistent   Capacity   Allocation   Available
----------------------------------------------------------------------------
exit=0

$ virsh -c qemu:///session net-list --all
 Name   State   Autostart   Persistent
----------------------------------------
exit=0
```

Consequently there are no session-pool volumes to enumerate. A read-only check of the conventional pool name confirms it is absent:

```text
$ virsh -c qemu:///session vol-list --pool default
error: failed to get pool 'default'
error: Storage pool not found: no storage pool with matching name 'default'
exit=1
```

No session storage pool is usable because none exists. No session libvirt network is usable because none exists. This does not prevent a future rootless VM from using a direct user-writable disk path and user-mode networking, but neither resource has been created or changed here.

The selected new domain name is `win10-mumu-test`; the direct name-conflict check found no such session domain:

```text
$ virsh -c qemu:///session dominfo win10-mumu-test
error: failed to get domain 'win10-mumu-test'
exit=1
```

The selected new disk path, if the ISO prerequisite is met, is `/ssd/win10-mumu-test.qcow2`. It does not exist and `/ssd` is writable by the current user:

```text
$ df -hT /ssd
Filesystem     Type  Size  Used  Avail Use% Mounted on
/dev/sda1      ext4  229G   32G  186G  15% /ssd

$ test -w /ssd; test ! -e /ssd/win10-mumu-test.qcow2
ssd_writable_exit=0
path_nonexistent_exit=0
```

Thus 186 GiB is available, exceeding the planned 80 GiB qcow2 virtual-disk capacity. No file at that path was created. The available UEFI firmware files were also verified read-only:

```text
$ ls -l /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_VARS_4M.fd
-rw-r--r-- 1 root root 3653632 ... /usr/share/OVMF/OVMF_CODE_4M.fd
-rw-r--r-- 1 root root  540672 ... /usr/share/OVMF/OVMF_VARS_4M.fd
exit=0

$ virt-install --connect qemu:///session --osinfo list | grep win10
win10
exit=0
```

## Windows 10 media evidence and blocker

The requested target did not exist before this attempt, and `/ssd` had sufficient free space. No ISO or partial download was created:

```text
$ df -hT /ssd
Filesystem     Type  Size  Used  Avail Use% Mounted on
/dev/sda1      ext4  229G   32G  186G  15% /ssd

$ test -e /ssd/Windows10_22H2_English_x64v1.iso
target_exists=no
```

The two supplied Microsoft-owned CDN candidates were probed directly, with all standard proxy environment variables unset. Each connection reached the CDN but returned HTTP 404, so neither candidate advertised an ISO content type or a plausible ISO content length. This was an availability/URL failure, not a proxy or other network transport error.

```text
$ env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY \
    curl -I -L --fail --max-time 30 \
    https://software-download.microsoft.com/pr/Win10_22H2_English_x64v1.iso
HTTP/1.1 404 Not Found
Server: Kestrel
Content-Length: 0
Date: Fri, 25 Sep 2026 18:50:26 GMT
Connection: keep-alive
X-OSID: 2
X-CID: 2
X-CCC: DE
curl: (22) The requested URL returned error: 404
exit=22

$ env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY \
    curl -I -L --fail --max-time 30 \
    https://software-download.microsoft.com/pr/Win10_22H2_EnglishInternational_x64v1.iso
HTTP/1.1 404 Not Found
Server: Kestrel
Content-Length: 0
Date: Fri, 25 Sep 2026 18:50:26 GMT
Connection: keep-alive
X-OSID: 2
X-CID: 2
X-CCC: DE
curl: (22) The requested URL returned error: 404
exit=22
```

Official URLs probed:

- <https://software-download.microsoft.com/pr/Win10_22H2_English_x64v1.iso>
- <https://software-download.microsoft.com/pr/Win10_22H2_EnglishInternational_x64v1.iso>

**Result: blocked.** No candidate was valid, so no large download, resumable `.part` file, rename, `file`/ISO9660 check, or checksum operation was performed. Final ISO path: none (the reserved target remains `/ssd/Windows10_22H2_English_x64v1.iso`). Final size: not applicable. SHA-256: not applicable; therefore there is no computed hash to independently compare with a Microsoft-published hash. No third-party URL was tried.

The exact blocker is that both provided direct official CDN candidate URLs returned `HTTP 404 Not Found` with `Content-Length: 0`. Do not use a third-party mirror, do not bypass licensing or activation, and do not download MuMu, a game, APK, ROM, or video before the Windows guest exists.

## VM, Windows, and MuMu evidence

**VM changes: none.** Although the rootless connection, unique name, direct disk destination, capacity, UEFI firmware, and `win10` OS metadata are available, the required legal ISO and checksum are absent. Therefore `virt-install` was not run and no disk, NVRAM file, domain XML, or VM was created.

Once the ISO is verified, the intended rootless VM must use `--connect qemu:///session`, name `win10-mumu-test`, a new qcow2 disk at `/ssd/win10-mumu-test.qcow2` with an 80 GiB capacity, exactly 4 vCPUs, 4096 MiB RAM, UEFI if supported by this session, host-passthrough CPU/VMX exposure if accepted, user-mode networking because the session has no libvirt network, SPICE or VNC graphics, and the official ISO attached. It must then be started and evidenced with `dominfo`, `vcpucount`, `dommemstat`, `domstate`, and relevant `dumpxml` fields.

**Windows boot evidence: none.** No guest exists, no ISO is attached, and no installer/desktop console has been displayed. Do not infer a Windows boot from the host checks.

**MuMu evidence: none.** MuMu was not downloaded, installed, or launched. After Windows is visibly usable, install it only inside the guest from MuMu's official publisher site and retain Android-main-screen evidence before claiming success.

## Next action and safe GUI handoff

The sole current blocker is the missing verified, legal Windows 10 ISO. After the user action above supplies it, rerun a local checksum comparison and create the rootless VM. If its graphical console does not open automatically, use the current user's desktop session:

1. Start `virt-manager` without elevation.
2. Add/select the `QEMU/KVM user session` connection (`qemu:///session`), not `QEMU/KVM system`.
3. Select `win10-mumu-test`, open its console, and complete the Windows installer from the attached official ISO.
4. Alternatively, after the VM has been created and started, run `virt-viewer --connect qemu:///session win10-mumu-test` (or use the SPICE/VNC console details shown by `virsh -c qemu:///session domdisplay win10-mumu-test`).

Leave a guest at the installer state if no GUI is available; do not claim Windows or MuMu completion without current console evidence.
