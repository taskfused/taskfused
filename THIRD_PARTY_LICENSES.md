# Third-party licenses

The TaskFused Go package, the taskfused, taskfused-worker and taskfused-demo
commands, their tests and their ready-built binaries include the code below.
TaskFused itself is licensed under the Apache License, Version 2.0 (see
LICENSE and NOTICE).

## go-sqlite3

github.com/mattn/go-sqlite3 v1.14.32, the SQLite driver TaskFused stores its
operations through.

```
The MIT License (MIT)

Copyright (c) 2014 Yasuhiro Matsumoto

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## SQLite

SQLite 3.50.4, compiled into TaskFused from the amalgamation that go-sqlite3
bundles. SQLite is in the public domain. Its source files carry this notice in
place of a license:

```
The author disclaims copyright to this source code.  In place of
a legal notice, here is a blessing:

   May you do good and not evil.
   May you find forgiveness for yourself and forgive others.
   May you share freely, never taking more than you give.
```

## Go

The Go runtime and standard library, compiled into every TaskFused binary.

```
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## musl libc

The Linux binaries are statically linked against musl libc, so its code is in
them. musl is licensed under the MIT License (some files are in the public
domain or under other permissive terms, listed in musl's COPYRIGHT file):

```
Copyright © 2005-2020 Rich Felker, et al.

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

## mingw-w64 runtime

The Windows binaries are built with mingw-w64 and statically include parts of
its C runtime. The notices below are those of the mingw-w64 runtime (CRT) as
packaged by Debian/Ubuntu (mingw-w64-common), for the runtime files and the
files under mingw-w64-crt with their own terms:

```
Files: *
Copyright: 2009-2016 the mingw-w64 project
License: ZPL-2.1
 Redistribution and use in source and binary forms, with or without
 modification, are permitted provided that the following conditions
 are met:
 .
    1. Redistributions in source code must retain the accompanying
       copyright notice, this list of conditions, and the following
       disclaimer.
    2. Redistributions in binary form must reproduce the accompanying
       copyright notice, this list of conditions, and the following
       disclaimer in the documentation and/or other materials provided
       with the distribution.
    3. Names of the copyright holders must not be used to endorse or
       promote products derived from this software without prior
       written permission from the copyright holders.
    4. The right to distribute this software or to use it for any
       purpose does not give you the right to use Servicemarks (sm) or
       Trademarks (tm) of the copyright holders.  Use of them is
       covered by separate agreement with the copyright holders.
    5. If any files are modified, you must cause the modified files to
       carry prominent notices stating that you changed the files and
       the date of any change.
 .
 Disclaimer
 .
 THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS ``AS IS'' AND ANY
 EXPRESSED OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
 IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
 PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDERS BE
 LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
 CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
 SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR
 BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
 WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE
 OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE,
 EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

Files: mingw-w64-crt/cfguard/*
       mingw-w64-crt/crt/*
       mingw-w64-crt/include/*
       mingw-w64-crt/intrincs/cpuid.c
       mingw-w64-crt/intrincs/in*.c
       mingw-w64-crt/intrincs/out*.c
       mingw-w64-crt/intrincs/rdtsc.c
       mingw-w64-crt/intrincs/readcr*.c
       mingw-w64-crt/intrincs/readmsr.c
       mingw-w64-crt/intrincs/writecr*.c
       mingw-w64-crt/intrincs/writemsr.c
       mingw-w64-crt/lib32/crtdll.def.in
       mingw-w64-crt/libsrc/agt*.c
       mingw-w64-crt/libsrc/cdo*.c
       mingw-w64-crt/libsrc/dxva-uuid.c
       mingw-w64-crt/libsrc/emostore_i.c
       mingw-w64-crt/libsrc/iisext_i.c
       mingw-w64-crt/libsrc/mtsadmin_i.c
       mingw-w64-crt/libsrc/mtxadmin_i.c
       mingw-w64-crt/libsrc/scard*.c
       mingw-w64-crt/libsrc/scrnsave.c
       mingw-w64-crt/libsrc/tsuserex_i.c
       mingw-w64-crt/math/cbrt*.c
       mingw-w64-crt/math/copysign*
       mingw-w64-crt/math/cosh*.c
       mingw-w64-crt/math/erfl.c
       mingw-w64-crt/math/expf.c
       mingw-w64-crt/math/fabs*.c
       mingw-w64-crt/math/fdim*.c
       mingw-w64-crt/math/fm*
       mingw-w64-crt/math/fp*.c
       mingw-w64-crt/math/i*
       mingw-w64-crt/math/ldexp*.c
       mingw-w64-crt/math/lgamma*.c
       mingw-w64-crt/math/llr*
       mingw-w64-crt/math/log10f.c
       mingw-w64-crt/math/logf.c
       mingw-w64-crt/math/lr*.c
       mingw-w64-crt/math/m*
       mingw-w64-crt/math/n*
       mingw-w64-crt/math/powf.c
       mingw-w64-crt/math/r*
       mingw-w64-crt/math/signbit*.c
       mingw-w64-crt/math/sinh*
       mingw-w64-crt/math/t*
       mingw-w64-crt/math/x86/acosf.c
       mingw-w64-crt/math/x86/acosl.c
       mingw-w64-crt/math/x86/asin*.c
       mingw-w64-crt/math/x86/atan*.c
       mingw-w64-crt/math/x86/ceil*.S
       mingw-w64-crt/math/x86/_chgsignl.S
       mingw-w64-crt/math/x86/cosf.c
       mingw-w64-crt/math/x86/cosl_internal.S
       mingw-w64-crt/math/x86/cossin.c
       mingw-w64-crt/math/x86/exp2*.S
       mingw-w64-crt/math/x86/fastmath.h
       mingw-w64-crt/math/x86/floor*.S
       mingw-w64-crt/math/x86/log*.S
       mingw-w64-crt/math/x86/logb*.c
       mingw-w64-crt/math/x86/scalbn*.S
       mingw-w64-crt/math/x86/sinf.c
       mingw-w64-crt/math/x86/sinl_internal.S
       mingw-w64-crt/misc/*
       mingw-w64-crt/profile/mcountFunc.S
       mingw-w64-crt/stdio/atoll.c
       mingw-w64-crt/stdio/_Exit.c
       mingw-w64-crt/stdio/fopen64.c
       mingw-w64-crt/stdio/fseeko64.c
       mingw-w64-crt/stdio/ftello64.c
       mingw-w64-crt/stdio/lltoa.c
       mingw-w64-crt/stdio/lltow.c
       mingw-w64-crt/stdio/lseek64.c
       mingw-w64-crt/stdio/mingw_fprintfw.c
       mingw-w64-crt/stdio/mingw_pformatw.c
       mingw-w64-crt/stdio/mingw_printfw.c
       mingw-w64-crt/stdio/mingw_snprintfw.c
       mingw-w64-crt/stdio/mingw_sprintfw.c
       mingw-w64-crt/stdio/mingw_vfprintfw.c
       mingw-w64-crt/stdio/mingw_vprintfw.c
       mingw-w64-crt/stdio/mingw_vsnprintfw.c
       mingw-w64-crt/stdio/mingw_vsprintfw.c
       mingw-w64-crt/stdio/snprintf.c
       mingw-w64-crt/stdio/snwprintf.c
       mingw-w64-crt/stdio/strtof.c
       mingw-w64-crt/stdio/ulltoa.c
       mingw-w64-crt/stdio/ulltow.c
       mingw-w64-crt/stdio/v*scanf*
       mingw-w64-crt/stdio/vs*printf*
       mingw-w64-crt/stdio/wtoll.c
       mingw-w64-crt/testcases/tstmainc.c
       mingw-w64-crt/testcases/tstmaincpp.cpp
       mingw-w64-crt/testcases/tstmain_sys_xxx.c
       mingw-w64-crt/testcases/tstwassert.c
       mingw-w64-crt/testcases/tstwassert_win.c
       mingw-w64-crt/testcases/tstwinmain.c
       mingw-w64-headers/crt/*
       mingw-w64-headers/ddk/include/d3dnthal.h
       mingw-w64-headers/ddk/include/ddk/afilter.h
       mingw-w64-headers/ddk/include/ddk/atm.h
       mingw-w64-headers/ddk/include/ddk/d4drvif.h
       mingw-w64-headers/ddk/include/ddkernel.h
       mingw-w64-headers/ddk/include/ddk/hidclass.h
       mingw-w64-headers/ddk/include/ddk/ide.h
       mingw-w64-headers/ddk/include/ddk/ioaccess.h
       mingw-w64-headers/ddk/include/ddk/kbdmou.h
       mingw-w64-headers/ddk/include/ddk/mcd.h
       mingw-w64-headers/ddk/include/ddk/miniport.h
       mingw-w64-headers/ddk/include/ddk/minitape.h
       mingw-w64-headers/ddk/include/ddk/ndis*.h
       mingw-w64-headers/ddk/include/ddk/ntagp.h
       mingw-w64-headers/ddk/include/ddk/ntddk.h
       mingw-w64-headers/ddk/include/ddk/ntddpcm.h
       mingw-w64-headers/ddk/include/ddk/ntddsnd.h
       mingw-w64-headers/ddk/include/ddk/ntifs.h
       mingw-w64-headers/ddk/include/ddk/ntpoapi.h
       mingw-w64-headers/ddk/include/ddk/ntstrsafe.h
       mingw-w64-headers/ddk/include/ddk/parallel.h
       mingw-w64-headers/ddk/include/ddk/pfhook.h
       mingw-w64-headers/ddk/include/ddk/poclass.h
       mingw-w64-headers/ddk/include/ddk/portcls.h
       mingw-w64-headers/ddk/include/ddk/punknown.h
       mingw-w64-headers/ddk/include/ddk/scsi*.h
       mingw-w64-headers/ddk/include/ddk/smbus.h
       mingw-w64-headers/ddk/include/ddk/srb.h
       mingw-w64-headers/ddk/include/ddk/stdunk.h
       mingw-w64-headers/ddk/include/ddk/storport.h
       mingw-w64-headers/ddk/include/ddk/tdikrnl.h
       mingw-w64-headers/ddk/include/ddk/tdistat.h
       mingw-w64-headers/ddk/include/ddk/upssvc.h
       mingw-w64-headers/ddk/include/ddk/usbscan.h
       mingw-w64-headers/ddk/include/ddk/video*.h
       mingw-w64-headers/ddk/include/ddk/wdm*.h
       mingw-w64-headers/ddk/include/ddk/ws2san.h
       mingw-w64-headers/ddk/include/ddk/xfilter.h
       mingw-w64-headers/ddk/include/ddrawint.h
       mingw-w64-headers/ddk/include/dmemmgr.h
       mingw-w64-headers/ddk/include/dvp.h
       mingw-w64-headers/include/*
Copyright: public-domain
License: public-domain

Files: mingw-w64-crt/gdtoa/*
       mingw-w64-crt/stdio/mingw_pformat.c
Copyright: 1997-2001 Lucent Technologies
License: Lucent
 Permission to use, copy, modify, and distribute this software and
 its documentation for any purpose and without fee is hereby
 granted, provided that the above copyright notice appear in all
 copies and that both that the copyright notice and this
 permission notice and warranty disclaimer appear in supporting
 documentation, and that the name of Lucent or any of its entities
 not be used in advertising or publicity pertaining to
 distribution of the software without specific, written prior
 permission.
 .
 LUCENT DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE,
 INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS.
 IN NO EVENT SHALL LUCENT OR ANY OF ITS ENTITIES BE LIABLE FOR ANY
 SPECIAL, INDIRECT OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER
 IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION,
 ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
 THIS SOFTWARE.

Files: mingw-w64-crt/gdtoa/qnan.c
       mingw-w64-crt/gdtoa/strtodnrp.c
Copyright: 2004, 2005 David M. Gay
           1998, 2000 Lucent Technologies
License: David-Gay
 Permission to use, copy, modify, and distribute this software and its
 documentation for any purpose and without fee is hereby granted,
 provided that the above copyright notice appear in all copies and
 that both that the copyright notice and this permission notice and
 warranty disclaimer appear in supporting documentation, and that the
 name of the author or any of his current or former employers not be
 used in advertising or publicity pertaining to distribution of the
 software without specific, written prior permission.
 .
 THE AUTHOR DISCLAIMS ALL WARRANTIES WITH REGARD TO THIS SOFTWARE,
 INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS.  IN
 NO EVENT SHALL THE AUTHOR OR ANY OF HIS CURRENT OR FORMER EMPLOYERS
 BE LIABLE FOR ANY SPECIAL, INDIRECT OR CONSEQUENTIAL DAMAGES OR ANY
 DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS,
 WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS
 ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE
 OF THIS SOFTWARE.

Files: mingw-w64-crt/math/s_erf.c
       mingw-w64-crt/math/sf_erf.c
       mingw-w64-crt/math/softmath/bsd_private.h
       mingw-w64-crt/math/softmath/bsd_privatef.h
       mingw-w64-crt/math/softmath/e_fmod.c
       mingw-w64-crt/math/softmath/e_fmodf.c
       mingw-w64-crt/math/softmath/e_pow.c
       mingw-w64-crt/math/softmath/e_powf.c
       mingw-w64-crt/math/softmath/e_remainder.c
       mingw-w64-crt/math/softmath/e_remainderf.c
       mingw-w64-crt/math/softmath/softmath_private.h
Copyright: 1993 Sun Microsystems, Inc.
License: Permissive-Sun
 Permission to use, copy, modify, and distribute this software is
 freely granted, provided that this notice is preserved.

Files: mingw-w64-crt/misc/basename.c
       mingw-w64-crt/misc/dirname.c
       mingw-w64-crt/stdio/mingw_fprintf.c
       mingw-w64-crt/stdio/mingw_pformat.*
       mingw-w64-crt/stdio/mingw_printf.c
       mingw-w64-crt/stdio/mingw_snprintf.c
       mingw-w64-crt/stdio/mingw_sprintf.c
       mingw-w64-crt/stdio/mingw_vfprintf.c
       mingw-w64-crt/stdio/mingw_vprintf.c
       mingw-w64-crt/stdio/mingw_vsnprintf.c
       mingw-w64-crt/stdio/mingw_vsprintf.c
Copyright: Keith Marshall
License: Keith-Marshall
 This is free software.  You may redistribute and/or modify it as you
 see fit, without restriction of copyright.
 .
 This software is provided "as is", in the hope that it may be useful,
 but WITHOUT WARRANTY OF ANY KIND, not even any implied warranty of
 MERCHANTABILITY, nor of FITNESS FOR ANY PARTICULAR PURPOSE.  At no
 time will the author accept any form of liability for any damages,
 however caused, resulting from the use of this software.

Files: mingw-w64-crt/misc/getopt.c
Copyright: 2000 The NetBSD Foundation, Inc.
           2002 Todd C. Miller <Todd.Miller@courtesan.com>
License: BSD-2-clause-NetBSD and ISC

Files: mingw-w64-crt/profile/gcrt0.c
       mingw-w64-crt/profile/profil.*
Copyright: 1998-2002 Red Hat, Inc.
License: Cygwin
 This program is free software; you can redistribute it and/or modify
 it under the terms of the GNU General Public License (GPL) as
 published by the Free Software Foundation; either version 2 of the
 License, or (at your option) any later version.
 .
 This program is distributed in the hope that it will be useful, but
 WITHOUT ANY WARRANTY; without even the implied warranty of
 MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 General Public License for more details.
 .
 You should have received a copy of the GNU General Public License
 along with this program; if not, write to the Free Software
 Foundation, Inc., 51 Franklin St, Fifth Floor, Boston, MA 02110-1301,
 USA.
 .
                         *** NOTE ***
 .
 In accordance with section 10 of the GPL, Red Hat permits programs
 whose sources are distributed under a license that complies with the
 Open Source definition to be linked with libcygwin.a without
 libcygwin.a itself causing the resulting program to be covered by the
 GNU GPL.
 .
 This means that you can port an Open Source(tm) application to
 cygwin, and distribute that executable as if it didn't include a copy
 of libcygwin.a linked into it.  Note that this does not apply to the
 cygwin DLL itself.  If you distribute a (possibly modified) version
 of the DLL you must adhere to the terms of the GPL, i.e. you must
 provide sources for the cygwin DLL.
 .
 See http://www.opensource.org/docs/definition_plain.html for the
 precise Open Source Definition referenced above.
 .
 Red Hat sells a special Cygwin License for customers who are unable
 to provide their application in open source code form.  For more
 information, please see:
 http://www.redhat.com/software/tools/cygwin/, or call 866-2REDHAT
 ext.  3007

Files: mingw-w64-crt/profile/gmon.*
       mingw-w64-crt/profile/mcount.c
Copyright: 1983, 1992, 1993 The Regents of the University of California
License: BSD-3-clause

Files: mingw-w64-crt/stdio/strtok_r.c
Copyright: 1988, 1993 The Regents of the University of California
           1998 Softweyr LLC
License: BSD-3-clause

License: BSD-3-clause
 Redistribution and use in source and binary forms, with or without
 modification, are permitted provided that the following conditions
 are met:
 1. Redistributions of source code must retain the above copyright
    notice, this list of conditions and the following disclaimer.
 2. Redistributions in binary form must reproduce the above copyright
    notice, this list of conditions and the following disclaimer in
    the documentation and/or other materials provided with the
    distribution.
 4. Neither the name of the University nor the names of its
    contributors may be used to endorse or promote products derived
    from this software without specific prior written permission.
 .
 THIS SOFTWARE IS PROVIDED BY THE REGENTS AND CONTRIBUTORS ``AS IS''
 AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO,
 THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A
 PARTICULAR PURPOSE ARE DISCLAIMED.  IN NO EVENT SHALL THE REGENTS OR
 CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
 EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
 PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
 PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY
 OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
 (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
 OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

License: BSD-2-clause-NetBSD
 Redistribution and use in source and binary forms, with or without
 modification, are permitted provided that the following conditions
 are met:
 1. Redistributions of source code must retain the above copyright
    notice, this list of conditions and the following disclaimer.
 2. Redistributions in binary form must reproduce the above copyright
    notice, this list of conditions and the following disclaimer in
    the documentation and/or other materials provided with the
    distribution.
 .
 THIS SOFTWARE IS PROVIDED BY THE NETBSD FOUNDATION, INC. AND
 CONTRIBUTORS ``AS IS'' AND ANY EXPRESS OR IMPLIED WARRANTIES,
 INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
 MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
 IN NO EVENT SHALL THE FOUNDATION OR CONTRIBUTORS BE LIABLE FOR ANY
 DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
 DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE
 GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
 INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER
 IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
 OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN
 IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

License: ISC
 Permission to use, copy, modify, and distribute this software for any
 purpose with or without fee is hereby granted, provided that the
 above copyright notice and this permission notice appear in all
 copies.
 .
 THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL
 WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED
 WARRANTIES OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE
 AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL
 DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR
 PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
 TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
 PERFORMANCE OF THIS SOFTWARE.

License: public-domain
 This file has no copyright assigned and is placed in the Public
 Domain.
```

## Operating system runtimes

The macOS and Windows binaries also link dynamically to the operating system's
own C runtime and system libraries, which are not copied into the archives.
