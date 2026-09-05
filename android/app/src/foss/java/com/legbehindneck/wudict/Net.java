// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// The FOSS flavour's listen policy: loopback by default, and a switch that
// puts the server on THIS network - at a concrete address, never a wildcard.
//
// One class of this name exists per flavour and NEVER in src/main, exactly as
// Storage does (D62). The Play build compiles the other one, which has no
// switch, no address walk and no "0.0.0.0" anywhere in it - so the control
// cannot appear on its settings screen, and the string it would need is not
// in its resources either (src/foss/res/values/strings.xml).
//
// WHY NOT 0.0.0.0, WHICH IS WHAT THIS USED TO PASS
//
// A wildcard bind is not "the LAN". Go resolves 0.0.0.0 to a DUAL-STACK [::]
// listen (net.favoriteAddrFamily returns AF_INET6 with IPV6_V6ONLY off, and
// ipToSockaddrInet6 rewrites the v4 wildcard), so the socket also answers on
// every IPv6 address the device holds. On Wi-Fi that is mostly link-local; on
// 4G/5G it is a GLOBALLY ROUTABLE address with no carrier NAT in front of it,
// which turns "share with my laptop" into an unauthenticated server on the
// public internet. Binding the one site-local IPv4 address the user actually
// meant keeps the feature and removes that entirely: nothing outside the
// local subnet can reach it, on any bearer.
//
// The cost is honest and accepted: the address is resolved once, at spawn.
// Rejoining a different network - or a DHCP change - leaves the server bound
// to an address the device no longer has, and sharing simply stops until the
// app is reopened. Repairing it live would need ACCESS_NETWORK_STATE and a
// callback, which is a permission and a background listener bought for a
// testing convenience. The settings screen already surfaces it for free: the
// stale check compares what a spawn WOULD pass against what the running
// server reports, and after a roam those differ, so the "restart" prompt
// appears on its own.
package com.legbehindneck.wudict;

import android.content.Context;

import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.net.SocketException;
import java.util.Collections;

final class Net {

    private Net() {
    }

    /**
     * The listen-address rows this flavour contributes to the settings screen.
     * Stored token: "0.0.0.0" - kept as the ON marker rather than migrated,
     * because it is what every installed copy already has in its preferences
     * and it never reaches argv now (see {@link #bindIp}).
     */
    static ShellPrefs.Override[] overrides() {
        return new ShellPrefs.Override[]{
                new ShellPrefs.Override("SERVER_IP", "--ip", ShellPrefs.BOOL,
                        "0.0.0.0", ShellPrefs.DEFAULT_IP, 0, 0,
                        R.string.settings_server_ip, R.string.settings_server_ip_hint),
        };
    }

    /**
     * What --ip gets. Loopback unless the switch is on AND this device has a
     * site-local address to offer: with the switch on and no network there is
     * nothing to bind to, and loopback is the answer that still starts.
     */
    static String bindIp(Context c) {
        if (ShellPrefs.override(c, ShellPrefs.byKey("SERVER_IP")) == null) {
            return ShellPrefs.DEFAULT_IP;
        }
        String lan = lanAddress();
        return lan == null ? ShellPrefs.DEFAULT_IP : lan;
    }

    /**
     * The address another device on this network would use, or null when there
     * is no such network. {@link NetworkInterface} and not
     * {@code ConnectivityManager}: the latter needs ACCESS_NETWORK_STATE, and
     * this is one address, not a reason to hold a permission. Reads the
     * kernel's own interface list - no network I/O, so no thread of its own.
     */
    static String lanAddress() {
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (ni.isLoopback() || !ni.isUp() || ni.isPointToPoint()) continue;
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    // Site-local IPv4 only: that is the whole promise of this
                    // switch - reachable from the same subnet and nowhere else.
                    if (a instanceof Inet4Address && a.isSiteLocalAddress()) {
                        return a.getHostAddress();
                    }
                }
            }
        } catch (SocketException | RuntimeException e) {
            // no interface list to read; the same answer as no network
        }
        return null;
    }
}
