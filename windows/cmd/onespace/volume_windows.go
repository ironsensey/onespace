//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PowerShell hosts this small managed COM bridge so the float audio-level argument
// uses the correct Windows x64 calling convention. It starts only on a volume change.
// Most of the time the background service consumes no extra audio-related CPU.
const volumeBridge = `
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
[ComImport, Guid("BCDE0395-E52F-467C-8E3D-C4579291692E")]
class MMDeviceEnumerator { }
[ComImport, Guid("A95664D2-9614-4F35-A746-DE8DB63617E6"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IMMDeviceEnumerator {
  void EnumAudioEndpoints(int dataFlow, int mask, out IntPtr devices);
  void GetDefaultAudioEndpoint(int dataFlow, int role, out IMMDevice device);
  void GetDevice(string id, out IMMDevice device);
  void RegisterEndpointNotificationCallback(IntPtr notify);
  void UnregisterEndpointNotificationCallback(IntPtr notify);
}
[ComImport, Guid("D666063F-1587-4E43-81F1-B948E807363F"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IMMDevice {
  void Activate(ref Guid iid, int clsCtx, IntPtr activationParams,
      [MarshalAs(UnmanagedType.Interface)] out IAudioEndpointVolume volume);
}
[ComImport, Guid("5CDF2C82-841E-4546-9722-0CF74078229A"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IAudioEndpointVolume {
  void RegisterControlChangeNotify(IntPtr notify);
  void UnregisterControlChangeNotify(IntPtr notify);
  void GetChannelCount(out uint channels);
  void SetMasterVolumeLevel(float decibels, IntPtr eventContext);
  void SetMasterVolumeLevelScalar(float level, IntPtr eventContext);
}
public class OneSpaceVolume {
  public static void Set(int percent) {
    var enumerator = (IMMDeviceEnumerator)new MMDeviceEnumerator();
    IMMDevice device;
    enumerator.GetDefaultAudioEndpoint(0, 1, out device);
    Guid iid = new Guid("5CDF2C82-841E-4546-9722-0CF74078229A");
    IAudioEndpointVolume volume;
    device.Activate(ref iid, 23, IntPtr.Zero, out volume);
    volume.SetMasterVolumeLevelScalar(percent / 100.0f, IntPtr.Zero);
    Marshal.ReleaseComObject(volume);
    Marshal.ReleaseComObject(device);
    Marshal.ReleaseComObject(enumerator);
  }
}
'@
[OneSpaceVolume]::Set(__PERCENT__)
`

func setSystemVolume(percent int) error {
	if percent < 0 || percent > 100 {
		return fmt.Errorf("volume must be 0–100")
	}
	command := strings.ReplaceAll(volumeBridge, "__PERCENT__", strconv.Itoa(percent))
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Windows Core Audio call failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
