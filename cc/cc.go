// Copyright 2015 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cc

// This file contains the module types for compiling C/C++ for Android, and converts the properties
// into the flags and filenames necessary to pass to the compiler.  The final creation of the rules
// is handled in builder.go

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"

	"android/soong"
	"android/soong/android"
	"android/soong/genrule"
)

func init() {
	soong.RegisterModuleType("cc_library_static", libraryStaticFactory)
	soong.RegisterModuleType("cc_library_shared", librarySharedFactory)
	soong.RegisterModuleType("cc_library", libraryFactory)
	soong.RegisterModuleType("cc_object", objectFactory)
	soong.RegisterModuleType("cc_binary", binaryFactory)
	soong.RegisterModuleType("cc_test", testFactory)
	soong.RegisterModuleType("cc_benchmark", benchmarkFactory)
	soong.RegisterModuleType("cc_defaults", defaultsFactory)

	soong.RegisterModuleType("toolchain_library", toolchainLibraryFactory)
	soong.RegisterModuleType("ndk_prebuilt_library", ndkPrebuiltLibraryFactory)
	soong.RegisterModuleType("ndk_prebuilt_object", ndkPrebuiltObjectFactory)
	soong.RegisterModuleType("ndk_prebuilt_static_stl", ndkPrebuiltStaticStlFactory)
	soong.RegisterModuleType("ndk_prebuilt_shared_stl", ndkPrebuiltSharedStlFactory)

	soong.RegisterModuleType("cc_library_host_static", libraryHostStaticFactory)
	soong.RegisterModuleType("cc_library_host_shared", libraryHostSharedFactory)
	soong.RegisterModuleType("cc_binary_host", binaryHostFactory)
	soong.RegisterModuleType("cc_test_host", testHostFactory)
	soong.RegisterModuleType("cc_benchmark_host", benchmarkHostFactory)

	// LinkageMutator must be registered after common.ArchMutator, but that is guaranteed by
	// the Go initialization order because this package depends on common, so common's init
	// functions will run first.
	android.RegisterBottomUpMutator("link", linkageMutator)
	android.RegisterBottomUpMutator("test_per_src", testPerSrcMutator)
	android.RegisterBottomUpMutator("deps", depsMutator)

	android.RegisterTopDownMutator("asan_deps", sanitizerDepsMutator(asan))
	android.RegisterBottomUpMutator("asan", sanitizerMutator(asan))

	android.RegisterTopDownMutator("tsan_deps", sanitizerDepsMutator(tsan))
	android.RegisterBottomUpMutator("tsan", sanitizerMutator(tsan))
}

var (
	HostPrebuiltTag = pctx.VariableConfigMethod("HostPrebuiltTag", android.Config.PrebuiltOS)

	LibcRoot = pctx.SourcePathVariable("LibcRoot", "bionic/libc")
)

// Flags used by lots of devices.  Putting them in package static variables will save bytes in
// build.ninja so they aren't repeated for every file
var (
	commonGlobalCflags = []string{
		"-DANDROID",
		"-fmessage-length=0",
		"-W",
		"-Wall",
		"-Wno-unused",
		"-Winit-self",
		"-Wpointer-arith",

		// COMMON_RELEASE_CFLAGS
		"-DNDEBUG",
		"-UDEBUG",
	}

	deviceGlobalCflags = []string{
		"-fdiagnostics-color",

		// TARGET_ERROR_FLAGS
		"-Werror=return-type",
		"-Werror=non-virtual-dtor",
		"-Werror=address",
		"-Werror=sequence-point",
		"-Werror=date-time",
	}

	hostGlobalCflags = []string{}

	commonGlobalCppflags = []string{
		"-Wsign-promo",
	}

	noOverrideGlobalCflags = []string{
		"-Werror=int-to-pointer-cast",
		"-Werror=pointer-to-int-cast",
	}

	illegalFlags = []string{
		"-w",
	}
)

func init() {
	if android.BuildOs == android.Linux {
		commonGlobalCflags = append(commonGlobalCflags, "-fdebug-prefix-map=/proc/self/cwd=")
	}

	pctx.StaticVariable("commonGlobalCflags", strings.Join(commonGlobalCflags, " "))
	pctx.StaticVariable("deviceGlobalCflags", strings.Join(deviceGlobalCflags, " "))
	pctx.StaticVariable("hostGlobalCflags", strings.Join(hostGlobalCflags, " "))
	pctx.StaticVariable("noOverrideGlobalCflags", strings.Join(noOverrideGlobalCflags, " "))

	pctx.StaticVariable("commonGlobalCppflags", strings.Join(commonGlobalCppflags, " "))

	pctx.StaticVariable("commonClangGlobalCflags",
		strings.Join(append(clangFilterUnknownCflags(commonGlobalCflags), "${clangExtraCflags}"), " "))
	pctx.StaticVariable("deviceClangGlobalCflags",
		strings.Join(append(clangFilterUnknownCflags(deviceGlobalCflags), "${clangExtraTargetCflags}"), " "))
	pctx.StaticVariable("hostClangGlobalCflags",
		strings.Join(clangFilterUnknownCflags(hostGlobalCflags), " "))
	pctx.StaticVariable("noOverrideClangGlobalCflags",
		strings.Join(append(clangFilterUnknownCflags(noOverrideGlobalCflags), "${clangExtraNoOverrideCflags}"), " "))

	pctx.StaticVariable("commonClangGlobalCppflags",
		strings.Join(append(clangFilterUnknownCflags(commonGlobalCppflags), "${clangExtraCppflags}"), " "))

	// Everything in this list is a crime against abstraction and dependency tracking.
	// Do not add anything to this list.
	pctx.PrefixedPathsForOptionalSourceVariable("commonGlobalIncludes", "-isystem ",
		[]string{
			"system/core/include",
			"system/media/audio/include",
			"hardware/libhardware/include",
			"hardware/libhardware_legacy/include",
			"hardware/ril/include",
			"libnativehelper/include",
			"frameworks/native/include",
			"frameworks/native/opengl/include",
			"frameworks/av/include",
			"frameworks/base/include",
		})
	// This is used by non-NDK modules to get jni.h. export_include_dirs doesn't help
	// with this, since there is no associated library.
	pctx.PrefixedPathsForOptionalSourceVariable("commonNativehelperInclude", "-I",
		[]string{"libnativehelper/include/nativehelper"})

	pctx.SourcePathVariable("clangDefaultBase", "prebuilts/clang/host")
	pctx.VariableFunc("clangBase", func(config interface{}) (string, error) {
		if override := config.(android.Config).Getenv("LLVM_PREBUILTS_BASE"); override != "" {
			return override, nil
		}
		return "${clangDefaultBase}", nil
	})
	pctx.VariableFunc("clangVersion", func(config interface{}) (string, error) {
		if override := config.(android.Config).Getenv("LLVM_PREBUILTS_VERSION"); override != "" {
			return override, nil
		}
		return "clang-2812033", nil
	})
	pctx.StaticVariable("clangPath", "${clangBase}/${HostPrebuiltTag}/${clangVersion}")
	pctx.StaticVariable("clangBin", "${clangPath}/bin")
}

type Deps struct {
	SharedLibs, LateSharedLibs                  []string
	StaticLibs, LateStaticLibs, WholeStaticLibs []string

	ReexportSharedLibHeaders, ReexportStaticLibHeaders []string

	ObjFiles []string

	GeneratedSources []string
	GeneratedHeaders []string

	Cflags, ReexportedCflags []string

	CrtBegin, CrtEnd string
}

type PathDeps struct {
	SharedLibs, LateSharedLibs                  android.Paths
	StaticLibs, LateStaticLibs, WholeStaticLibs android.Paths

	ObjFiles               android.Paths
	WholeStaticLibObjFiles android.Paths

	GeneratedSources android.Paths
	GeneratedHeaders android.Paths

	Cflags, ReexportedCflags []string

	CrtBegin, CrtEnd android.OptionalPath
}

type Flags struct {
	GlobalFlags []string // Flags that apply to C, C++, and assembly source files
	AsFlags     []string // Flags that apply to assembly source files
	CFlags      []string // Flags that apply to C and C++ source files
	ConlyFlags  []string // Flags that apply to C source files
	CppFlags    []string // Flags that apply to C++ source files
	YaccFlags   []string // Flags that apply to Yacc source files
	LdFlags     []string // Flags that apply to linker command lines
	libFlags    []string // Flags to add libraries early to the link order

	Nocrt     bool
	Toolchain Toolchain
	Clang     bool

	RequiredInstructionSet string
	DynamicLinker          string

	CFlagsDeps android.Paths // Files depended on by compiler flags
}

type BaseCompilerProperties struct {
	// list of source files used to compile the C/C++ module.  May be .c, .cpp, or .S files.
	Srcs []string `android:"arch_variant"`

	// list of source files that should not be used to build the C/C++ module.
	// This is most useful in the arch/multilib variants to remove non-common files
	Exclude_srcs []string `android:"arch_variant"`

	// list of module-specific flags that will be used for C and C++ compiles.
	Cflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for C++ compiles
	Cppflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for C compiles
	Conlyflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for .S compiles
	Asflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for C and C++ compiles when
	// compiling with clang
	Clang_cflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for .S compiles when
	// compiling with clang
	Clang_asflags []string `android:"arch_variant"`

	// list of module-specific flags that will be used for .y and .yy compiles
	Yaccflags []string

	// the instruction set architecture to use to compile the C/C++
	// module.
	Instruction_set string `android:"arch_variant"`

	// list of directories relative to the root of the source tree that will
	// be added to the include path using -I.
	// If possible, don't use this.  If adding paths from the current directory use
	// local_include_dirs, if adding paths from other modules use export_include_dirs in
	// that module.
	Include_dirs []string `android:"arch_variant"`

	// list of directories relative to the Blueprints file that will
	// be added to the include path using -I
	Local_include_dirs []string `android:"arch_variant"`

	// list of generated sources to compile. These are the names of gensrcs or
	// genrule modules.
	Generated_sources []string `android:"arch_variant"`

	// list of generated headers to add to the include path. These are the names
	// of genrule modules.
	Generated_headers []string `android:"arch_variant"`

	// pass -frtti instead of -fno-rtti
	Rtti *bool

	Debug, Release struct {
		// list of module-specific flags that will be used for C and C++ compiles in debug or
		// release builds
		Cflags []string `android:"arch_variant"`
	} `android:"arch_variant"`
}

type BaseLinkerProperties struct {
	// list of modules whose object files should be linked into this module
	// in their entirety.  For static library modules, all of the .o files from the intermediate
	// directory of the dependency will be linked into this modules .a file.  For a shared library,
	// the dependency's .a file will be linked into this module using -Wl,--whole-archive.
	Whole_static_libs []string `android:"arch_variant,variant_prepend"`

	// list of modules that should be statically linked into this module.
	Static_libs []string `android:"arch_variant,variant_prepend"`

	// list of modules that should be dynamically linked into this module.
	Shared_libs []string `android:"arch_variant"`

	// list of module-specific flags that will be used for all link steps
	Ldflags []string `android:"arch_variant"`

	// don't insert default compiler flags into asflags, cflags,
	// cppflags, conlyflags, ldflags, or include_dirs
	No_default_compiler_flags *bool

	// list of system libraries that will be dynamically linked to
	// shared library and executable modules.  If unset, generally defaults to libc
	// and libm.  Set to [] to prevent linking against libc and libm.
	System_shared_libs []string

	// allow the module to contain undefined symbols.  By default,
	// modules cannot contain undefined symbols that are not satisified by their immediate
	// dependencies.  Set this flag to true to remove --no-undefined from the linker flags.
	// This flag should only be necessary for compiling low-level libraries like libc.
	Allow_undefined_symbols *bool

	// don't link in libgcc.a
	No_libgcc *bool

	// -l arguments to pass to linker for host-provided shared libraries
	Host_ldlibs []string `android:"arch_variant"`

	// list of shared libraries to re-export include directories from. Entries must be
	// present in shared_libs.
	Export_shared_lib_headers []string `android:"arch_variant"`

	// list of static libraries to re-export include directories from. Entries must be
	// present in static_libs.
	Export_static_lib_headers []string `android:"arch_variant"`
}

type LibraryCompilerProperties struct {
	Static struct {
		Srcs         []string `android:"arch_variant"`
		Exclude_srcs []string `android:"arch_variant"`
		Cflags       []string `android:"arch_variant"`
	} `android:"arch_variant"`
	Shared struct {
		Srcs         []string `android:"arch_variant"`
		Exclude_srcs []string `android:"arch_variant"`
		Cflags       []string `android:"arch_variant"`
	} `android:"arch_variant"`
}

type FlagExporterProperties struct {
	// list of directories relative to the Blueprints file that will
	// be added to the include path using -I for any module that links against this module
	Export_include_dirs []string `android:"arch_variant"`
}

type LibraryLinkerProperties struct {
	Static struct {
		Enabled           *bool    `android:"arch_variant"`
		Whole_static_libs []string `android:"arch_variant"`
		Static_libs       []string `android:"arch_variant"`
		Shared_libs       []string `android:"arch_variant"`
	} `android:"arch_variant"`
	Shared struct {
		Enabled           *bool    `android:"arch_variant"`
		Whole_static_libs []string `android:"arch_variant"`
		Static_libs       []string `android:"arch_variant"`
		Shared_libs       []string `android:"arch_variant"`
	} `android:"arch_variant"`

	// local file name to pass to the linker as --version_script
	Version_script *string `android:"arch_variant"`
	// local file name to pass to the linker as -unexported_symbols_list
	Unexported_symbols_list *string `android:"arch_variant"`
	// local file name to pass to the linker as -force_symbols_not_weak_list
	Force_symbols_not_weak_list *string `android:"arch_variant"`
	// local file name to pass to the linker as -force_symbols_weak_list
	Force_symbols_weak_list *string `android:"arch_variant"`

	// don't link in crt_begin and crt_end.  This flag should only be necessary for
	// compiling crt or libc.
	Nocrt *bool `android:"arch_variant"`

	VariantName string `blueprint:"mutated"`
}

type BinaryLinkerProperties struct {
	// compile executable with -static
	Static_executable *bool

	// set the name of the output
	Stem string `android:"arch_variant"`

	// append to the name of the output
	Suffix string `android:"arch_variant"`

	// if set, add an extra objcopy --prefix-symbols= step
	Prefix_symbols string
}

type TestLinkerProperties struct {
	// if set, build against the gtest library. Defaults to true.
	Gtest bool

	// Create a separate binary for each source file.  Useful when there is
	// global state that can not be torn down and reset between each test suite.
	Test_per_src *bool
}

type ObjectLinkerProperties struct {
	// names of other cc_object modules to link into this module using partial linking
	Objs []string `android:"arch_variant"`
}

// Properties used to compile all C or C++ modules
type BaseProperties struct {
	// compile module with clang instead of gcc
	Clang *bool `android:"arch_variant"`

	// Minimum sdk version supported when compiling against the ndk
	Sdk_version string

	// don't insert default compiler flags into asflags, cflags,
	// cppflags, conlyflags, ldflags, or include_dirs
	No_default_compiler_flags *bool

	AndroidMkSharedLibs []string `blueprint:"mutated"`
	HideFromMake        bool     `blueprint:"mutated"`
}

type InstallerProperties struct {
	// install to a subdirectory of the default install path for the module
	Relative_install_path string
}

type StripProperties struct {
	Strip struct {
		None         bool
		Keep_symbols bool
	}
}

type UnusedProperties struct {
	Native_coverage *bool
	Required        []string
	Tags            []string
}

type ModuleContextIntf interface {
	module() *Module
	static() bool
	staticBinary() bool
	clang() bool
	toolchain() Toolchain
	noDefaultCompilerFlags() bool
	sdk() bool
	sdkVersion() string
	selectedStl() string
}

type ModuleContext interface {
	android.ModuleContext
	ModuleContextIntf
}

type BaseModuleContext interface {
	android.BaseContext
	ModuleContextIntf
}

type Customizer interface {
	CustomizeProperties(BaseModuleContext)
	Properties() []interface{}
}

type feature interface {
	begin(ctx BaseModuleContext)
	deps(ctx BaseModuleContext, deps Deps) Deps
	flags(ctx ModuleContext, flags Flags) Flags
	props() []interface{}
}

type compiler interface {
	feature
	compile(ctx ModuleContext, flags Flags, deps PathDeps) android.Paths
}

type linker interface {
	feature
	link(ctx ModuleContext, flags Flags, deps PathDeps, objFiles android.Paths) android.Path
	installable() bool
}

type installer interface {
	props() []interface{}
	install(ctx ModuleContext, path android.Path)
	inData() bool
}

type dependencyTag struct {
	blueprint.BaseDependencyTag
	name    string
	library bool

	reexportFlags bool
}

var (
	sharedDepTag       = dependencyTag{name: "shared", library: true}
	sharedExportDepTag = dependencyTag{name: "shared", library: true, reexportFlags: true}
	lateSharedDepTag   = dependencyTag{name: "late shared", library: true}
	staticDepTag       = dependencyTag{name: "static", library: true}
	staticExportDepTag = dependencyTag{name: "static", library: true, reexportFlags: true}
	lateStaticDepTag   = dependencyTag{name: "late static", library: true}
	wholeStaticDepTag  = dependencyTag{name: "whole static", library: true, reexportFlags: true}
	genSourceDepTag    = dependencyTag{name: "gen source"}
	genHeaderDepTag    = dependencyTag{name: "gen header"}
	objDepTag          = dependencyTag{name: "obj"}
	crtBeginDepTag     = dependencyTag{name: "crtbegin"}
	crtEndDepTag       = dependencyTag{name: "crtend"}
	reuseObjTag        = dependencyTag{name: "reuse objects"}
)

// Module contains the properties and members used by all C/C++ module types, and implements
// the blueprint.Module interface.  It delegates to compiler, linker, and installer interfaces
// to construct the output file.  Behavior can be customized with a Customizer interface
type Module struct {
	android.ModuleBase
	android.DefaultableModule

	Properties BaseProperties
	unused     UnusedProperties

	// initialize before calling Init
	hod      android.HostOrDeviceSupported
	multilib android.Multilib

	// delegates, initialize before calling Init
	customizer Customizer
	features   []feature
	compiler   compiler
	linker     linker
	installer  installer
	stl        *stl
	sanitize   *sanitize

	androidMkSharedLibDeps []string

	outputFile android.OptionalPath

	cachedToolchain Toolchain
}

func (c *Module) Init() (blueprint.Module, []interface{}) {
	props := []interface{}{&c.Properties, &c.unused}
	if c.customizer != nil {
		props = append(props, c.customizer.Properties()...)
	}
	if c.compiler != nil {
		props = append(props, c.compiler.props()...)
	}
	if c.linker != nil {
		props = append(props, c.linker.props()...)
	}
	if c.installer != nil {
		props = append(props, c.installer.props()...)
	}
	if c.stl != nil {
		props = append(props, c.stl.props()...)
	}
	if c.sanitize != nil {
		props = append(props, c.sanitize.props()...)
	}
	for _, feature := range c.features {
		props = append(props, feature.props()...)
	}

	_, props = android.InitAndroidArchModule(c, c.hod, c.multilib, props...)

	return android.InitDefaultableModule(c, c, props...)
}

type baseModuleContext struct {
	android.BaseContext
	moduleContextImpl
}

type moduleContext struct {
	android.ModuleContext
	moduleContextImpl
}

type moduleContextImpl struct {
	mod *Module
	ctx BaseModuleContext
}

func (ctx *moduleContextImpl) module() *Module {
	return ctx.mod
}

func (ctx *moduleContextImpl) clang() bool {
	return ctx.mod.clang(ctx.ctx)
}

func (ctx *moduleContextImpl) toolchain() Toolchain {
	return ctx.mod.toolchain(ctx.ctx)
}

func (ctx *moduleContextImpl) static() bool {
	if ctx.mod.linker == nil {
		panic(fmt.Errorf("static called on module %q with no linker", ctx.ctx.ModuleName()))
	}
	if linker, ok := ctx.mod.linker.(baseLinkerInterface); ok {
		return linker.static()
	} else {
		panic(fmt.Errorf("static called on module %q that doesn't use base linker", ctx.ctx.ModuleName()))
	}
}

func (ctx *moduleContextImpl) staticBinary() bool {
	if ctx.mod.linker == nil {
		panic(fmt.Errorf("staticBinary called on module %q with no linker", ctx.ctx.ModuleName()))
	}
	if linker, ok := ctx.mod.linker.(baseLinkerInterface); ok {
		return linker.staticBinary()
	} else {
		panic(fmt.Errorf("staticBinary called on module %q that doesn't use base linker", ctx.ctx.ModuleName()))
	}
}

func (ctx *moduleContextImpl) noDefaultCompilerFlags() bool {
	return Bool(ctx.mod.Properties.No_default_compiler_flags)
}

func (ctx *moduleContextImpl) sdk() bool {
	if ctx.ctx.Device() {
		return ctx.mod.Properties.Sdk_version != ""
	}
	return false
}

func (ctx *moduleContextImpl) sdkVersion() string {
	if ctx.ctx.Device() {
		return ctx.mod.Properties.Sdk_version
	}
	return ""
}

func (ctx *moduleContextImpl) selectedStl() string {
	if stl := ctx.mod.stl; stl != nil {
		return stl.Properties.SelectedStl
	}
	return ""
}

func newBaseModule(hod android.HostOrDeviceSupported, multilib android.Multilib) *Module {
	return &Module{
		hod:      hod,
		multilib: multilib,
	}
}

func newModule(hod android.HostOrDeviceSupported, multilib android.Multilib) *Module {
	module := newBaseModule(hod, multilib)
	module.stl = &stl{}
	module.sanitize = &sanitize{}
	return module
}

func (c *Module) GenerateAndroidBuildActions(actx android.ModuleContext) {
	ctx := &moduleContext{
		ModuleContext: actx,
		moduleContextImpl: moduleContextImpl{
			mod: c,
		},
	}
	ctx.ctx = ctx

	flags := Flags{
		Toolchain: c.toolchain(ctx),
		Clang:     c.clang(ctx),
	}
	if c.compiler != nil {
		flags = c.compiler.flags(ctx, flags)
	}
	if c.linker != nil {
		flags = c.linker.flags(ctx, flags)
	}
	if c.stl != nil {
		flags = c.stl.flags(ctx, flags)
	}
	if c.sanitize != nil {
		flags = c.sanitize.flags(ctx, flags)
	}
	for _, feature := range c.features {
		flags = feature.flags(ctx, flags)
	}
	if ctx.Failed() {
		return
	}

	flags.CFlags, _ = filterList(flags.CFlags, illegalFlags)
	flags.CppFlags, _ = filterList(flags.CppFlags, illegalFlags)
	flags.ConlyFlags, _ = filterList(flags.ConlyFlags, illegalFlags)

	// Optimization to reduce size of build.ninja
	// Replace the long list of flags for each file with a module-local variable
	ctx.Variable(pctx, "cflags", strings.Join(flags.CFlags, " "))
	ctx.Variable(pctx, "cppflags", strings.Join(flags.CppFlags, " "))
	ctx.Variable(pctx, "asflags", strings.Join(flags.AsFlags, " "))
	flags.CFlags = []string{"$cflags"}
	flags.CppFlags = []string{"$cppflags"}
	flags.AsFlags = []string{"$asflags"}

	deps := c.depsToPaths(ctx)
	if ctx.Failed() {
		return
	}

	flags.CFlags = append(flags.CFlags, deps.Cflags...)

	var objFiles android.Paths
	if c.compiler != nil {
		objFiles = c.compiler.compile(ctx, flags, deps)
		if ctx.Failed() {
			return
		}
	}

	if c.linker != nil {
		outputFile := c.linker.link(ctx, flags, deps, objFiles)
		if ctx.Failed() {
			return
		}
		c.outputFile = android.OptionalPathForPath(outputFile)

		if c.installer != nil && c.linker.installable() {
			c.installer.install(ctx, outputFile)
			if ctx.Failed() {
				return
			}
		}
	}
}

func (c *Module) toolchain(ctx BaseModuleContext) Toolchain {
	if c.cachedToolchain == nil {
		arch := ctx.Arch()
		os := ctx.Os()
		factory := toolchainFactories[os][arch.ArchType]
		if factory == nil {
			ctx.ModuleErrorf("Toolchain not found for %s arch %q", os.String(), arch.String())
			return nil
		}
		c.cachedToolchain = factory(arch)
	}
	return c.cachedToolchain
}

func (c *Module) begin(ctx BaseModuleContext) {
	if c.compiler != nil {
		c.compiler.begin(ctx)
	}
	if c.linker != nil {
		c.linker.begin(ctx)
	}
	if c.stl != nil {
		c.stl.begin(ctx)
	}
	if c.sanitize != nil {
		c.sanitize.begin(ctx)
	}
	for _, feature := range c.features {
		feature.begin(ctx)
	}
}

func (c *Module) deps(ctx BaseModuleContext) Deps {
	deps := Deps{}

	if c.compiler != nil {
		deps = c.compiler.deps(ctx, deps)
	}
	if c.linker != nil {
		deps = c.linker.deps(ctx, deps)
	}
	if c.stl != nil {
		deps = c.stl.deps(ctx, deps)
	}
	if c.sanitize != nil {
		deps = c.sanitize.deps(ctx, deps)
	}
	for _, feature := range c.features {
		deps = feature.deps(ctx, deps)
	}

	deps.WholeStaticLibs = lastUniqueElements(deps.WholeStaticLibs)
	deps.StaticLibs = lastUniqueElements(deps.StaticLibs)
	deps.LateStaticLibs = lastUniqueElements(deps.LateStaticLibs)
	deps.SharedLibs = lastUniqueElements(deps.SharedLibs)
	deps.LateSharedLibs = lastUniqueElements(deps.LateSharedLibs)

	for _, lib := range deps.ReexportSharedLibHeaders {
		if !inList(lib, deps.SharedLibs) {
			ctx.PropertyErrorf("export_shared_lib_headers", "Shared library not in shared_libs: '%s'", lib)
		}
	}

	for _, lib := range deps.ReexportStaticLibHeaders {
		if !inList(lib, deps.StaticLibs) {
			ctx.PropertyErrorf("export_static_lib_headers", "Static library not in static_libs: '%s'", lib)
		}
	}

	return deps
}

func (c *Module) depsMutator(actx android.BottomUpMutatorContext) {
	ctx := &baseModuleContext{
		BaseContext: actx,
		moduleContextImpl: moduleContextImpl{
			mod: c,
		},
	}
	ctx.ctx = ctx

	if c.customizer != nil {
		c.customizer.CustomizeProperties(ctx)
	}

	c.begin(ctx)

	deps := c.deps(ctx)

	c.Properties.AndroidMkSharedLibs = deps.SharedLibs

	actx.AddVariationDependencies([]blueprint.Variation{{"link", "static"}}, wholeStaticDepTag,
		deps.WholeStaticLibs...)

	for _, lib := range deps.StaticLibs {
		depTag := staticDepTag
		if inList(lib, deps.ReexportStaticLibHeaders) {
			depTag = staticExportDepTag
		}
		actx.AddVariationDependencies([]blueprint.Variation{{"link", "static"}}, depTag,
			deps.StaticLibs...)
	}

	actx.AddVariationDependencies([]blueprint.Variation{{"link", "static"}}, lateStaticDepTag,
		deps.LateStaticLibs...)

	for _, lib := range deps.SharedLibs {
		depTag := sharedDepTag
		if inList(lib, deps.ReexportSharedLibHeaders) {
			depTag = sharedExportDepTag
		}
		actx.AddVariationDependencies([]blueprint.Variation{{"link", "shared"}}, depTag,
			deps.SharedLibs...)
	}

	actx.AddVariationDependencies([]blueprint.Variation{{"link", "shared"}}, lateSharedDepTag,
		deps.LateSharedLibs...)

	actx.AddDependency(ctx.module(), genSourceDepTag, deps.GeneratedSources...)
	actx.AddDependency(ctx.module(), genHeaderDepTag, deps.GeneratedHeaders...)

	actx.AddDependency(ctx.module(), objDepTag, deps.ObjFiles...)

	if deps.CrtBegin != "" {
		actx.AddDependency(ctx.module(), crtBeginDepTag, deps.CrtBegin)
	}
	if deps.CrtEnd != "" {
		actx.AddDependency(ctx.module(), crtEndDepTag, deps.CrtEnd)
	}
}

func depsMutator(ctx android.BottomUpMutatorContext) {
	if c, ok := ctx.Module().(*Module); ok {
		c.depsMutator(ctx)
	}
}

func (c *Module) clang(ctx BaseModuleContext) bool {
	clang := Bool(c.Properties.Clang)

	if c.Properties.Clang == nil {
		if ctx.Host() {
			clang = true
		}

		if ctx.Device() && ctx.AConfig().DeviceUsesClang() {
			clang = true
		}
	}

	if !c.toolchain(ctx).ClangSupported() {
		clang = false
	}

	return clang
}

// Convert dependencies to paths.  Returns a PathDeps containing paths
func (c *Module) depsToPaths(ctx android.ModuleContext) PathDeps {
	var depPaths PathDeps

	// Whether a module can link to another module, taking into
	// account NDK linking.
	linkTypeOk := func(from, to *Module) bool {
		if from.Target().Os != android.Android {
			// Host code is not restricted
			return true
		}
		if from.Properties.Sdk_version == "" {
			// Platform code can link to anything
			return true
		}
		if _, ok := to.linker.(*toolchainLibraryLinker); ok {
			// These are always allowed
			return true
		}
		if _, ok := to.linker.(*ndkPrebuiltLibraryLinker); ok {
			// These are allowed, but don't set sdk_version
			return true
		}
		if _, ok := to.linker.(*ndkPrebuiltStlLinker); ok {
			// These are allowed, but don't set sdk_version
			return true
		}
		return to.Properties.Sdk_version != ""
	}

	ctx.VisitDirectDeps(func(m blueprint.Module) {
		name := ctx.OtherModuleName(m)
		tag := ctx.OtherModuleDependencyTag(m)

		a, _ := m.(android.Module)
		if a == nil {
			ctx.ModuleErrorf("module %q not an android module", name)
			return
		}

		cc, _ := m.(*Module)
		if cc == nil {
			switch tag {
			case android.DefaultsDepTag:
			case genSourceDepTag:
				if genRule, ok := m.(genrule.SourceFileGenerator); ok {
					depPaths.GeneratedSources = append(depPaths.GeneratedSources,
						genRule.GeneratedSourceFiles()...)
				} else {
					ctx.ModuleErrorf("module %q is not a gensrcs or genrule", name)
				}
			case genHeaderDepTag:
				if genRule, ok := m.(genrule.SourceFileGenerator); ok {
					depPaths.GeneratedHeaders = append(depPaths.GeneratedHeaders,
						genRule.GeneratedSourceFiles()...)
					depPaths.Cflags = append(depPaths.Cflags,
						includeDirsToFlags(android.Paths{genRule.GeneratedHeaderDir()}))
				} else {
					ctx.ModuleErrorf("module %q is not a genrule", name)
				}
			default:
				ctx.ModuleErrorf("depends on non-cc module %q", name)
			}
			return
		}

		if !a.Enabled() {
			ctx.ModuleErrorf("depends on disabled module %q", name)
			return
		}

		if a.Target().Os != ctx.Os() {
			ctx.ModuleErrorf("OS mismatch between %q and %q", ctx.ModuleName(), name)
			return
		}

		if a.Target().Arch.ArchType != ctx.Arch().ArchType {
			ctx.ModuleErrorf("Arch mismatch between %q and %q", ctx.ModuleName(), name)
			return
		}

		if !cc.outputFile.Valid() {
			ctx.ModuleErrorf("module %q missing output file", name)
			return
		}

		if tag == reuseObjTag {
			depPaths.ObjFiles = append(depPaths.ObjFiles,
				cc.compiler.(*libraryCompiler).reuseObjFiles...)
			return
		}

		if t, ok := tag.(dependencyTag); ok && t.library {
			if i, ok := cc.linker.(exportedFlagsProducer); ok {
				cflags := i.exportedFlags()
				depPaths.Cflags = append(depPaths.Cflags, cflags...)

				if t.reexportFlags {
					depPaths.ReexportedCflags = append(depPaths.ReexportedCflags, cflags...)
				}
			}

			if !linkTypeOk(c, cc) {
				ctx.ModuleErrorf("depends on non-NDK-built library %q", name)
			}
		}

		var depPtr *android.Paths

		switch tag {
		case sharedDepTag, sharedExportDepTag:
			depPtr = &depPaths.SharedLibs
		case lateSharedDepTag:
			depPtr = &depPaths.LateSharedLibs
		case staticDepTag, staticExportDepTag:
			depPtr = &depPaths.StaticLibs
		case lateStaticDepTag:
			depPtr = &depPaths.LateStaticLibs
		case wholeStaticDepTag:
			depPtr = &depPaths.WholeStaticLibs
			staticLib, _ := cc.linker.(*libraryLinker)
			if staticLib == nil || !staticLib.static() {
				ctx.ModuleErrorf("module %q not a static library", name)
				return
			}

			if missingDeps := staticLib.getWholeStaticMissingDeps(); missingDeps != nil {
				postfix := " (required by " + ctx.OtherModuleName(m) + ")"
				for i := range missingDeps {
					missingDeps[i] += postfix
				}
				ctx.AddMissingDependencies(missingDeps)
			}
			depPaths.WholeStaticLibObjFiles =
				append(depPaths.WholeStaticLibObjFiles, staticLib.objFiles...)
		case objDepTag:
			depPtr = &depPaths.ObjFiles
		case crtBeginDepTag:
			depPaths.CrtBegin = cc.outputFile
		case crtEndDepTag:
			depPaths.CrtEnd = cc.outputFile
		default:
			panic(fmt.Errorf("unknown dependency tag: %s", tag))
		}

		if depPtr != nil {
			*depPtr = append(*depPtr, cc.outputFile.Path())
		}
	})

	return depPaths
}

func (c *Module) InstallInData() bool {
	if c.installer == nil {
		return false
	}
	return c.installer.inData()
}

// Compiler

type baseCompiler struct {
	Properties BaseCompilerProperties
}

var _ compiler = (*baseCompiler)(nil)

func (compiler *baseCompiler) props() []interface{} {
	return []interface{}{&compiler.Properties}
}

func (compiler *baseCompiler) begin(ctx BaseModuleContext) {}

func (compiler *baseCompiler) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps.GeneratedSources = append(deps.GeneratedSources, compiler.Properties.Generated_sources...)
	deps.GeneratedHeaders = append(deps.GeneratedHeaders, compiler.Properties.Generated_headers...)

	return deps
}

// Create a Flags struct that collects the compile flags from global values,
// per-target values, module type values, and per-module Blueprints properties
func (compiler *baseCompiler) flags(ctx ModuleContext, flags Flags) Flags {
	toolchain := ctx.toolchain()

	CheckBadCompilerFlags(ctx, "cflags", compiler.Properties.Cflags)
	CheckBadCompilerFlags(ctx, "cppflags", compiler.Properties.Cppflags)
	CheckBadCompilerFlags(ctx, "conlyflags", compiler.Properties.Conlyflags)
	CheckBadCompilerFlags(ctx, "asflags", compiler.Properties.Asflags)

	flags.CFlags = append(flags.CFlags, compiler.Properties.Cflags...)
	flags.CppFlags = append(flags.CppFlags, compiler.Properties.Cppflags...)
	flags.ConlyFlags = append(flags.ConlyFlags, compiler.Properties.Conlyflags...)
	flags.AsFlags = append(flags.AsFlags, compiler.Properties.Asflags...)
	flags.YaccFlags = append(flags.YaccFlags, compiler.Properties.Yaccflags...)

	// Include dir cflags
	rootIncludeDirs := android.PathsForSource(ctx, compiler.Properties.Include_dirs)
	localIncludeDirs := android.PathsForModuleSrc(ctx, compiler.Properties.Local_include_dirs)
	flags.GlobalFlags = append(flags.GlobalFlags,
		includeDirsToFlags(localIncludeDirs),
		includeDirsToFlags(rootIncludeDirs))

	if !ctx.noDefaultCompilerFlags() {
		if !ctx.sdk() || ctx.Host() {
			flags.GlobalFlags = append(flags.GlobalFlags,
				"${commonGlobalIncludes}",
				toolchain.IncludeFlags(),
				"${commonNativehelperInclude}")
		}

		flags.GlobalFlags = append(flags.GlobalFlags, []string{
			"-I" + android.PathForModuleSrc(ctx).String(),
			"-I" + android.PathForModuleOut(ctx).String(),
			"-I" + android.PathForModuleGen(ctx).String(),
		}...)
	}

	instructionSet := compiler.Properties.Instruction_set
	if flags.RequiredInstructionSet != "" {
		instructionSet = flags.RequiredInstructionSet
	}
	instructionSetFlags, err := toolchain.InstructionSetFlags(instructionSet)
	if flags.Clang {
		instructionSetFlags, err = toolchain.ClangInstructionSetFlags(instructionSet)
	}
	if err != nil {
		ctx.ModuleErrorf("%s", err)
	}

	CheckBadCompilerFlags(ctx, "release.cflags", compiler.Properties.Release.Cflags)

	// TODO: debug
	flags.CFlags = append(flags.CFlags, compiler.Properties.Release.Cflags...)

	if flags.Clang {
		CheckBadCompilerFlags(ctx, "clang_cflags", compiler.Properties.Clang_cflags)
		CheckBadCompilerFlags(ctx, "clang_asflags", compiler.Properties.Clang_asflags)

		flags.CFlags = clangFilterUnknownCflags(flags.CFlags)
		flags.CFlags = append(flags.CFlags, compiler.Properties.Clang_cflags...)
		flags.AsFlags = append(flags.AsFlags, compiler.Properties.Clang_asflags...)
		flags.CppFlags = clangFilterUnknownCflags(flags.CppFlags)
		flags.ConlyFlags = clangFilterUnknownCflags(flags.ConlyFlags)
		flags.LdFlags = clangFilterUnknownCflags(flags.LdFlags)

		target := "-target " + toolchain.ClangTriple()
		var gccPrefix string
		if !ctx.Darwin() {
			gccPrefix = "-B" + filepath.Join(toolchain.GccRoot(), toolchain.GccTriple(), "bin")
		}

		flags.CFlags = append(flags.CFlags, target, gccPrefix)
		flags.AsFlags = append(flags.AsFlags, target, gccPrefix)
		flags.LdFlags = append(flags.LdFlags, target, gccPrefix)
	}

	hod := "host"
	if ctx.Os().Class == android.Device {
		hod = "device"
	}

	if !ctx.noDefaultCompilerFlags() {
		flags.GlobalFlags = append(flags.GlobalFlags, instructionSetFlags)

		if flags.Clang {
			flags.AsFlags = append(flags.AsFlags, toolchain.ClangAsflags())
			flags.CppFlags = append(flags.CppFlags, "${commonClangGlobalCppflags}")
			flags.GlobalFlags = append(flags.GlobalFlags,
				toolchain.ClangCflags(),
				"${commonClangGlobalCflags}",
				fmt.Sprintf("${%sClangGlobalCflags}", hod))

			flags.ConlyFlags = append(flags.ConlyFlags, "${clangExtraConlyflags}")
		} else {
			flags.CppFlags = append(flags.CppFlags, "${commonGlobalCppflags}")
			flags.GlobalFlags = append(flags.GlobalFlags,
				toolchain.Cflags(),
				"${commonGlobalCflags}",
				fmt.Sprintf("${%sGlobalCflags}", hod))
		}

		if Bool(ctx.AConfig().ProductVariables.Brillo) {
			flags.GlobalFlags = append(flags.GlobalFlags, "-D__BRILLO__")
		}

		if ctx.Device() {
			if Bool(compiler.Properties.Rtti) {
				flags.CppFlags = append(flags.CppFlags, "-frtti")
			} else {
				flags.CppFlags = append(flags.CppFlags, "-fno-rtti")
			}
		}

		flags.AsFlags = append(flags.AsFlags, "-D__ASSEMBLY__")

		if flags.Clang {
			flags.CppFlags = append(flags.CppFlags, toolchain.ClangCppflags())
		} else {
			flags.CppFlags = append(flags.CppFlags, toolchain.Cppflags())
		}
	}

	if flags.Clang {
		flags.GlobalFlags = append(flags.GlobalFlags, toolchain.ToolchainClangCflags())
	} else {
		flags.GlobalFlags = append(flags.GlobalFlags, toolchain.ToolchainCflags())
	}

	if !ctx.sdk() {
		if ctx.Host() && !flags.Clang {
			// The host GCC doesn't support C++14 (and is deprecated, so likely
			// never will). Build these modules with C++11.
			flags.CppFlags = append(flags.CppFlags, "-std=gnu++11")
		} else {
			flags.CppFlags = append(flags.CppFlags, "-std=gnu++14")
		}
	}

	// We can enforce some rules more strictly in the code we own. strict
	// indicates if this is code that we can be stricter with. If we have
	// rules that we want to apply to *our* code (but maybe can't for
	// vendor/device specific things), we could extend this to be a ternary
	// value.
	strict := true
	if strings.HasPrefix(android.PathForModuleSrc(ctx).String(), "external/") {
		strict = false
	}

	// Can be used to make some annotations stricter for code we can fix
	// (such as when we mark functions as deprecated).
	if strict {
		flags.CFlags = append(flags.CFlags, "-DANDROID_STRICT")
	}

	return flags
}

func (compiler *baseCompiler) compile(ctx ModuleContext, flags Flags, deps PathDeps) android.Paths {
	// Compile files listed in c.Properties.Srcs into objects
	objFiles := compiler.compileObjs(ctx, flags, "",
		compiler.Properties.Srcs, compiler.Properties.Exclude_srcs,
		deps.GeneratedSources, deps.GeneratedHeaders)

	if ctx.Failed() {
		return nil
	}

	return objFiles
}

// Compile a list of source files into objects a specified subdirectory
func (compiler *baseCompiler) compileObjs(ctx android.ModuleContext, flags Flags,
	subdir string, srcFiles, excludes []string, extraSrcs, deps android.Paths) android.Paths {

	buildFlags := flagsToBuilderFlags(flags)

	inputFiles := ctx.ExpandSources(srcFiles, excludes)
	inputFiles = append(inputFiles, extraSrcs...)
	srcPaths, gendeps := genSources(ctx, inputFiles, buildFlags)

	deps = append(deps, gendeps...)
	deps = append(deps, flags.CFlagsDeps...)

	return TransformSourceToObj(ctx, subdir, srcPaths, buildFlags, deps)
}

// baseLinker provides support for shared_libs, static_libs, and whole_static_libs properties
type baseLinker struct {
	Properties        BaseLinkerProperties
	dynamicProperties struct {
		VariantIsShared       bool     `blueprint:"mutated"`
		VariantIsStatic       bool     `blueprint:"mutated"`
		VariantIsStaticBinary bool     `blueprint:"mutated"`
		RunPaths              []string `blueprint:"mutated"`
	}
}

func (linker *baseLinker) begin(ctx BaseModuleContext) {
	if ctx.toolchain().Is64Bit() {
		linker.dynamicProperties.RunPaths = []string{"../lib64", "lib64"}
	} else {
		linker.dynamicProperties.RunPaths = []string{"../lib", "lib"}
	}
}

func (linker *baseLinker) props() []interface{} {
	return []interface{}{&linker.Properties, &linker.dynamicProperties}
}

func (linker *baseLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps.WholeStaticLibs = append(deps.WholeStaticLibs, linker.Properties.Whole_static_libs...)
	deps.StaticLibs = append(deps.StaticLibs, linker.Properties.Static_libs...)
	deps.SharedLibs = append(deps.SharedLibs, linker.Properties.Shared_libs...)

	deps.ReexportStaticLibHeaders = append(deps.ReexportStaticLibHeaders, linker.Properties.Export_static_lib_headers...)
	deps.ReexportSharedLibHeaders = append(deps.ReexportSharedLibHeaders, linker.Properties.Export_shared_lib_headers...)

	if !ctx.sdk() && ctx.ModuleName() != "libcompiler_rt-extras" {
		deps.StaticLibs = append(deps.StaticLibs, "libcompiler_rt-extras")
	}

	if ctx.Device() {
		// libgcc and libatomic have to be last on the command line
		deps.LateStaticLibs = append(deps.LateStaticLibs, "libatomic")
		if !Bool(linker.Properties.No_libgcc) {
			deps.LateStaticLibs = append(deps.LateStaticLibs, "libgcc")
		}

		if !linker.static() {
			if linker.Properties.System_shared_libs != nil {
				deps.LateSharedLibs = append(deps.LateSharedLibs,
					linker.Properties.System_shared_libs...)
			} else if !ctx.sdk() {
				deps.LateSharedLibs = append(deps.LateSharedLibs, "libc", "libm")
			}
		}

		if ctx.sdk() {
			version := ctx.sdkVersion()
			deps.SharedLibs = append(deps.SharedLibs,
				"ndk_libc."+version,
				"ndk_libm."+version,
			)
		}
	}

	return deps
}

func (linker *baseLinker) flags(ctx ModuleContext, flags Flags) Flags {
	toolchain := ctx.toolchain()

	if !ctx.noDefaultCompilerFlags() {
		if ctx.Device() && !Bool(linker.Properties.Allow_undefined_symbols) {
			flags.LdFlags = append(flags.LdFlags, "-Wl,--no-undefined")
		}

		if flags.Clang {
			flags.LdFlags = append(flags.LdFlags, toolchain.ClangLdflags())
		} else {
			flags.LdFlags = append(flags.LdFlags, toolchain.Ldflags())
		}

		if ctx.Host() {
			CheckBadHostLdlibs(ctx, "host_ldlibs", linker.Properties.Host_ldlibs)

			flags.LdFlags = append(flags.LdFlags, linker.Properties.Host_ldlibs...)
		}
	}

	CheckBadLinkerFlags(ctx, "ldflags", linker.Properties.Ldflags)

	flags.LdFlags = append(flags.LdFlags, linker.Properties.Ldflags...)

	if ctx.Host() && !linker.static() {
		rpath_prefix := `\$$ORIGIN/`
		if ctx.Darwin() {
			rpath_prefix = "@loader_path/"
		}

		for _, rpath := range linker.dynamicProperties.RunPaths {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-rpath,"+rpath_prefix+rpath)
		}
	}

	if flags.Clang {
		flags.LdFlags = append(flags.LdFlags, toolchain.ToolchainClangLdflags())
	} else {
		flags.LdFlags = append(flags.LdFlags, toolchain.ToolchainLdflags())
	}

	return flags
}

func (linker *baseLinker) static() bool {
	return linker.dynamicProperties.VariantIsStatic
}

func (linker *baseLinker) staticBinary() bool {
	return linker.dynamicProperties.VariantIsStaticBinary
}

func (linker *baseLinker) setStatic(static bool) {
	linker.dynamicProperties.VariantIsStatic = static
}

func (linker *baseLinker) isDependencyRoot() bool {
	return false
}

type baseLinkerInterface interface {
	// Returns true if the build options for the module have selected a static or shared build
	buildStatic() bool
	buildShared() bool

	// Sets whether a specific variant is static or shared
	setStatic(bool)

	// Returns whether a specific variant is a static library or binary
	static() bool

	// Returns whether a module is a static binary
	staticBinary() bool

	// Returns true for dependency roots (binaries)
	// TODO(ccross): also handle dlopenable libraries
	isDependencyRoot() bool
}

type baseInstaller struct {
	Properties InstallerProperties

	dir   string
	dir64 string
	data  bool

	path android.OutputPath
}

var _ installer = (*baseInstaller)(nil)

func (installer *baseInstaller) props() []interface{} {
	return []interface{}{&installer.Properties}
}

func (installer *baseInstaller) install(ctx ModuleContext, file android.Path) {
	subDir := installer.dir
	if ctx.toolchain().Is64Bit() && installer.dir64 != "" {
		subDir = installer.dir64
	}
	if !ctx.Host() && !ctx.Arch().Native {
		subDir = filepath.Join(subDir, ctx.Arch().ArchType.String())
	}
	dir := android.PathForModuleInstall(ctx, subDir, installer.Properties.Relative_install_path)
	installer.path = ctx.InstallFile(dir, file)
}

func (installer *baseInstaller) inData() bool {
	return installer.data
}

//
// Combined static+shared libraries
//

type flagExporter struct {
	Properties FlagExporterProperties

	flags []string
}

func (f *flagExporter) exportIncludes(ctx ModuleContext, inc string) {
	includeDirs := android.PathsForModuleSrc(ctx, f.Properties.Export_include_dirs)
	f.flags = append(f.flags, android.JoinWithPrefix(includeDirs.Strings(), inc))
}

func (f *flagExporter) reexportFlags(flags []string) {
	f.flags = append(f.flags, flags...)
}

func (f *flagExporter) exportedFlags() []string {
	return f.flags
}

type exportedFlagsProducer interface {
	exportedFlags() []string
}

var _ exportedFlagsProducer = (*flagExporter)(nil)

type libraryCompiler struct {
	baseCompiler

	linker     *libraryLinker
	Properties LibraryCompilerProperties

	// For reusing static library objects for shared library
	reuseObjFiles android.Paths
}

var _ compiler = (*libraryCompiler)(nil)

func (library *libraryCompiler) props() []interface{} {
	props := library.baseCompiler.props()
	return append(props, &library.Properties)
}

func (library *libraryCompiler) flags(ctx ModuleContext, flags Flags) Flags {
	flags = library.baseCompiler.flags(ctx, flags)

	// MinGW spits out warnings about -fPIC even for -fpie?!) being ignored because
	// all code is position independent, and then those warnings get promoted to
	// errors.
	if ctx.Os() != android.Windows {
		flags.CFlags = append(flags.CFlags, "-fPIC")
	}

	if library.linker.static() {
		flags.CFlags = append(flags.CFlags, library.Properties.Static.Cflags...)
	} else {
		flags.CFlags = append(flags.CFlags, library.Properties.Shared.Cflags...)
	}

	return flags
}

func (library *libraryCompiler) compile(ctx ModuleContext, flags Flags, deps PathDeps) android.Paths {
	var objFiles android.Paths

	objFiles = library.baseCompiler.compile(ctx, flags, deps)
	library.reuseObjFiles = objFiles

	if library.linker.static() {
		objFiles = append(objFiles, library.compileObjs(ctx, flags, android.DeviceStaticLibrary,
			library.Properties.Static.Srcs, library.Properties.Static.Exclude_srcs,
			nil, deps.GeneratedHeaders)...)
	} else {
		objFiles = append(objFiles, library.compileObjs(ctx, flags, android.DeviceSharedLibrary,
			library.Properties.Shared.Srcs, library.Properties.Shared.Exclude_srcs,
			nil, deps.GeneratedHeaders)...)
	}

	return objFiles
}

type libraryLinker struct {
	baseLinker
	flagExporter
	stripper

	Properties LibraryLinkerProperties

	dynamicProperties struct {
		BuildStatic bool `blueprint:"mutated"`
		BuildShared bool `blueprint:"mutated"`
	}

	// If we're used as a whole_static_lib, our missing dependencies need
	// to be given
	wholeStaticMissingDeps []string

	// For whole_static_libs
	objFiles android.Paths
}

var _ linker = (*libraryLinker)(nil)

func (library *libraryLinker) begin(ctx BaseModuleContext) {
	library.baseLinker.begin(ctx)
	if library.static() {
		if library.Properties.Static.Enabled != nil &&
		 !*library.Properties.Static.Enabled {
			ctx.module().Disable()
		}
	} else {
		if library.Properties.Shared.Enabled != nil &&
		 !*library.Properties.Shared.Enabled {
			ctx.module().Disable()
		}
	}
}

func (library *libraryLinker) props() []interface{} {
	props := library.baseLinker.props()
	return append(props,
		&library.Properties,
		&library.dynamicProperties,
		&library.flagExporter.Properties,
		&library.stripper.StripProperties)
}

func (library *libraryLinker) flags(ctx ModuleContext, flags Flags) Flags {
	flags = library.baseLinker.flags(ctx, flags)

	flags.Nocrt = Bool(library.Properties.Nocrt)

	if !library.static() {
		libName := ctx.ModuleName() + library.Properties.VariantName
		// GCC for Android assumes that -shared means -Bsymbolic, use -Wl,-shared instead
		sharedFlag := "-Wl,-shared"
		if flags.Clang || ctx.Host() {
			sharedFlag = "-shared"
		}
		if ctx.Device() {
			flags.LdFlags = append(flags.LdFlags,
				"-nostdlib",
				"-Wl,--gc-sections",
			)
		}

		if ctx.Darwin() {
			flags.LdFlags = append(flags.LdFlags,
				"-dynamiclib",
				"-single_module",
				//"-read_only_relocs suppress",
				"-install_name @rpath/"+libName+flags.Toolchain.ShlibSuffix(),
			)
		} else {
			flags.LdFlags = append(flags.LdFlags,
				sharedFlag,
				"-Wl,-soname,"+libName+flags.Toolchain.ShlibSuffix(),
			)
		}
	}

	return flags
}

func (library *libraryLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps = library.baseLinker.deps(ctx, deps)
	if library.static() {
		deps.WholeStaticLibs = append(deps.WholeStaticLibs, library.Properties.Static.Whole_static_libs...)
		deps.StaticLibs = append(deps.StaticLibs, library.Properties.Static.Static_libs...)
		deps.SharedLibs = append(deps.SharedLibs, library.Properties.Static.Shared_libs...)
	} else {
		if ctx.Device() && !Bool(library.Properties.Nocrt) {
			if !ctx.sdk() {
				deps.CrtBegin = "crtbegin_so"
				deps.CrtEnd = "crtend_so"
			} else {
				deps.CrtBegin = "ndk_crtbegin_so." + ctx.sdkVersion()
				deps.CrtEnd = "ndk_crtend_so." + ctx.sdkVersion()
			}
		}
		deps.WholeStaticLibs = append(deps.WholeStaticLibs, library.Properties.Shared.Whole_static_libs...)
		deps.StaticLibs = append(deps.StaticLibs, library.Properties.Shared.Static_libs...)
		deps.SharedLibs = append(deps.SharedLibs, library.Properties.Shared.Shared_libs...)
	}

	return deps
}

func (library *libraryLinker) linkStatic(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	library.objFiles = append(android.Paths{}, deps.WholeStaticLibObjFiles...)
	library.objFiles = append(library.objFiles, objFiles...)

	outputFile := android.PathForModuleOut(ctx,
		ctx.ModuleName()+library.Properties.VariantName+staticLibraryExtension)

	if ctx.Darwin() {
		TransformDarwinObjToStaticLib(ctx, library.objFiles, flagsToBuilderFlags(flags), outputFile)
	} else {
		TransformObjToStaticLib(ctx, library.objFiles, flagsToBuilderFlags(flags), outputFile)
	}

	library.wholeStaticMissingDeps = ctx.GetMissingDependencies()

	ctx.CheckbuildFile(outputFile)

	return outputFile
}

func (library *libraryLinker) linkShared(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	var linkerDeps android.Paths

	versionScript := android.OptionalPathForModuleSrc(ctx, library.Properties.Version_script)
	unexportedSymbols := android.OptionalPathForModuleSrc(ctx, library.Properties.Unexported_symbols_list)
	forceNotWeakSymbols := android.OptionalPathForModuleSrc(ctx, library.Properties.Force_symbols_not_weak_list)
	forceWeakSymbols := android.OptionalPathForModuleSrc(ctx, library.Properties.Force_symbols_weak_list)
	if !ctx.Darwin() {
		if versionScript.Valid() {
			flags.LdFlags = append(flags.LdFlags, "-Wl,--version-script,"+versionScript.String())
			linkerDeps = append(linkerDeps, versionScript.Path())
		}
		if unexportedSymbols.Valid() {
			ctx.PropertyErrorf("unexported_symbols_list", "Only supported on Darwin")
		}
		if forceNotWeakSymbols.Valid() {
			ctx.PropertyErrorf("force_symbols_not_weak_list", "Only supported on Darwin")
		}
		if forceWeakSymbols.Valid() {
			ctx.PropertyErrorf("force_symbols_weak_list", "Only supported on Darwin")
		}
	} else {
		if versionScript.Valid() {
			ctx.PropertyErrorf("version_script", "Not supported on Darwin")
		}
		if unexportedSymbols.Valid() {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-unexported_symbols_list,"+unexportedSymbols.String())
			linkerDeps = append(linkerDeps, unexportedSymbols.Path())
		}
		if forceNotWeakSymbols.Valid() {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-force_symbols_not_weak_list,"+forceNotWeakSymbols.String())
			linkerDeps = append(linkerDeps, forceNotWeakSymbols.Path())
		}
		if forceWeakSymbols.Valid() {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-force_symbols_weak_list,"+forceWeakSymbols.String())
			linkerDeps = append(linkerDeps, forceWeakSymbols.Path())
		}
	}

	fileName := ctx.ModuleName() + library.Properties.VariantName + flags.Toolchain.ShlibSuffix()
	outputFile := android.PathForModuleOut(ctx, fileName)
	ret := outputFile

	builderFlags := flagsToBuilderFlags(flags)

	if library.stripper.needsStrip(ctx) {
		strippedOutputFile := outputFile
		outputFile = android.PathForModuleOut(ctx, "unstripped", fileName)
		library.stripper.strip(ctx, outputFile, strippedOutputFile, builderFlags)
	}

	sharedLibs := deps.SharedLibs
	sharedLibs = append(sharedLibs, deps.LateSharedLibs...)

	TransformObjToDynamicBinary(ctx, objFiles, sharedLibs,
		deps.StaticLibs, deps.LateStaticLibs, deps.WholeStaticLibs,
		linkerDeps, deps.CrtBegin, deps.CrtEnd, false, builderFlags, outputFile)

	return ret
}

func (library *libraryLinker) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	objFiles = append(objFiles, deps.ObjFiles...)

	var out android.Path
	if library.static() {
		out = library.linkStatic(ctx, flags, deps, objFiles)
	} else {
		out = library.linkShared(ctx, flags, deps, objFiles)
	}

	library.exportIncludes(ctx, "-I")
	library.reexportFlags(deps.ReexportedCflags)

	return out
}

func (library *libraryLinker) buildStatic() bool {
	return library.dynamicProperties.BuildStatic
}

func (library *libraryLinker) buildShared() bool {
	return library.dynamicProperties.BuildShared
}

func (library *libraryLinker) getWholeStaticMissingDeps() []string {
	return library.wholeStaticMissingDeps
}

func (library *libraryLinker) installable() bool {
	return !library.static()
}

type libraryInstaller struct {
	baseInstaller

	linker   *libraryLinker
	sanitize *sanitize
}

func (library *libraryInstaller) install(ctx ModuleContext, file android.Path) {
	if !library.linker.static() {
		library.baseInstaller.install(ctx, file)
	}
}

func (library *libraryInstaller) inData() bool {
	return library.baseInstaller.inData() || library.sanitize.inData()
}

func NewLibrary(hod android.HostOrDeviceSupported, shared, static bool) *Module {
	module := newModule(hod, android.MultilibBoth)

	linker := &libraryLinker{}
	linker.dynamicProperties.BuildShared = shared
	linker.dynamicProperties.BuildStatic = static
	module.linker = linker

	module.compiler = &libraryCompiler{
		linker: linker,
	}
	module.installer = &libraryInstaller{
		baseInstaller: baseInstaller{
			dir:   "lib",
			dir64: "lib64",
		},
		linker:   linker,
		sanitize: module.sanitize,
	}

	return module
}

func libraryFactory() (blueprint.Module, []interface{}) {
	module := NewLibrary(android.HostAndDeviceSupported, true, true)
	return module.Init()
}

//
// Objects (for crt*.o)
//

type objectLinker struct {
	Properties ObjectLinkerProperties
}

func objectFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	module.compiler = &baseCompiler{}
	module.linker = &objectLinker{}
	return module.Init()
}

func (object *objectLinker) props() []interface{} {
	return []interface{}{&object.Properties}
}

func (*objectLinker) begin(ctx BaseModuleContext) {}

func (object *objectLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps.ObjFiles = append(deps.ObjFiles, object.Properties.Objs...)
	return deps
}

func (*objectLinker) flags(ctx ModuleContext, flags Flags) Flags {
	if flags.Clang {
		flags.LdFlags = append(flags.LdFlags, ctx.toolchain().ToolchainClangLdflags())
	} else {
		flags.LdFlags = append(flags.LdFlags, ctx.toolchain().ToolchainLdflags())
	}

	return flags
}

func (object *objectLinker) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	objFiles = append(objFiles, deps.ObjFiles...)

	var outputFile android.Path
	if len(objFiles) == 1 {
		outputFile = objFiles[0]
	} else {
		output := android.PathForModuleOut(ctx, ctx.ModuleName()+objectExtension)
		TransformObjsToObj(ctx, objFiles, flagsToBuilderFlags(flags), output)
		outputFile = output
	}

	ctx.CheckbuildFile(outputFile)
	return outputFile
}

func (*objectLinker) installable() bool {
	return false
}

//
// Executables
//

type binaryLinker struct {
	baseLinker
	stripper

	Properties BinaryLinkerProperties

	hostToolPath android.OptionalPath
}

var _ linker = (*binaryLinker)(nil)

func (binary *binaryLinker) props() []interface{} {
	return append(binary.baseLinker.props(),
		&binary.Properties,
		&binary.stripper.StripProperties)

}

func (binary *binaryLinker) buildStatic() bool {
	return binary.baseLinker.staticBinary()
}

func (binary *binaryLinker) buildShared() bool {
	return !binary.baseLinker.staticBinary()
}

func (binary *binaryLinker) getStem(ctx BaseModuleContext) string {
	stem := ctx.ModuleName()
	if binary.Properties.Stem != "" {
		stem = binary.Properties.Stem
	}

	return stem + binary.Properties.Suffix
}

func (binary *binaryLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps = binary.baseLinker.deps(ctx, deps)
	if ctx.Device() {
		if !ctx.sdk() {
			if binary.buildStatic() {
				deps.CrtBegin = "crtbegin_static"
			} else {
				deps.CrtBegin = "crtbegin_dynamic"
			}
			deps.CrtEnd = "crtend_android"
		} else {
			if binary.buildStatic() {
				deps.CrtBegin = "ndk_crtbegin_static." + ctx.sdkVersion()
			} else {
				deps.CrtBegin = "ndk_crtbegin_dynamic." + ctx.sdkVersion()
			}
			deps.CrtEnd = "ndk_crtend_android." + ctx.sdkVersion()
		}

		if binary.buildStatic() {
			if inList("libc++_static", deps.StaticLibs) {
				deps.StaticLibs = append(deps.StaticLibs, "libm", "libc", "libdl")
			}
			// static libraries libcompiler_rt, libc and libc_nomalloc need to be linked with
			// --start-group/--end-group along with libgcc.  If they are in deps.StaticLibs,
			// move them to the beginning of deps.LateStaticLibs
			var groupLibs []string
			deps.StaticLibs, groupLibs = filterList(deps.StaticLibs,
				[]string{"libc", "libc_nomalloc", "libcompiler_rt"})
			deps.LateStaticLibs = append(groupLibs, deps.LateStaticLibs...)
		}
	}

	if binary.buildShared() && inList("libc", deps.StaticLibs) {
		ctx.ModuleErrorf("statically linking libc to dynamic executable, please remove libc\n" +
			"from static libs or set static_executable: true")
	}
	return deps
}

func (*binaryLinker) installable() bool {
	return true
}

func (binary *binaryLinker) isDependencyRoot() bool {
	return true
}

func NewBinary(hod android.HostOrDeviceSupported) *Module {
	module := newModule(hod, android.MultilibFirst)
	module.compiler = &baseCompiler{}
	module.linker = &binaryLinker{}
	module.installer = &baseInstaller{
		dir: "bin",
	}
	return module
}

func binaryFactory() (blueprint.Module, []interface{}) {
	module := NewBinary(android.HostAndDeviceSupported)
	return module.Init()
}

func (binary *binaryLinker) begin(ctx BaseModuleContext) {
	binary.baseLinker.begin(ctx)

	static := Bool(binary.Properties.Static_executable)
	if ctx.Host() {
		if ctx.Os() == android.Linux {
			if binary.Properties.Static_executable == nil && Bool(ctx.AConfig().ProductVariables.HostStaticBinaries) {
				static = true
			}
		} else {
			// Static executables are not supported on Darwin or Windows
			static = false
		}
	}
	if static {
		binary.dynamicProperties.VariantIsStatic = true
		binary.dynamicProperties.VariantIsStaticBinary = true
	}
}

func (binary *binaryLinker) flags(ctx ModuleContext, flags Flags) Flags {
	flags = binary.baseLinker.flags(ctx, flags)

	if ctx.Host() && !binary.staticBinary() {
		flags.LdFlags = append(flags.LdFlags, "-pie")
		if ctx.Os() == android.Windows {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-e_mainCRTStartup")
		}
	}

	// MinGW spits out warnings about -fPIC even for -fpie?!) being ignored because
	// all code is position independent, and then those warnings get promoted to
	// errors.
	if ctx.Os() != android.Windows {
		flags.CFlags = append(flags.CFlags, "-fpie")
	}

	if ctx.Device() {
		if binary.buildStatic() {
			// Clang driver needs -static to create static executable.
			// However, bionic/linker uses -shared to overwrite.
			// Linker for x86 targets does not allow coexistance of -static and -shared,
			// so we add -static only if -shared is not used.
			if !inList("-shared", flags.LdFlags) {
				flags.LdFlags = append(flags.LdFlags, "-static")
			}

			flags.LdFlags = append(flags.LdFlags,
				"-nostdlib",
				"-Bstatic",
				"-Wl,--gc-sections",
			)

		} else {
			if flags.DynamicLinker == "" {
				flags.DynamicLinker = "/system/bin/linker"
				if flags.Toolchain.Is64Bit() {
					flags.DynamicLinker += "64"
				}
			}

			flags.LdFlags = append(flags.LdFlags,
				"-pie",
				"-nostdlib",
				"-Bdynamic",
				"-Wl,--gc-sections",
				"-Wl,-z,nocopyreloc",
			)
		}
	} else {
		if binary.staticBinary() {
			flags.LdFlags = append(flags.LdFlags, "-static")
		}
		if ctx.Darwin() {
			flags.LdFlags = append(flags.LdFlags, "-Wl,-headerpad_max_install_names")
		}
	}

	return flags
}

func (binary *binaryLinker) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	fileName := binary.getStem(ctx) + flags.Toolchain.ExecutableSuffix()
	outputFile := android.PathForModuleOut(ctx, fileName)
	ret := outputFile
	if ctx.Os().Class == android.Host {
		binary.hostToolPath = android.OptionalPathForPath(outputFile)
	}

	var linkerDeps android.Paths

	sharedLibs := deps.SharedLibs
	sharedLibs = append(sharedLibs, deps.LateSharedLibs...)

	if flags.DynamicLinker != "" {
		flags.LdFlags = append(flags.LdFlags, " -Wl,-dynamic-linker,"+flags.DynamicLinker)
	}

	builderFlags := flagsToBuilderFlags(flags)

	if binary.stripper.needsStrip(ctx) {
		strippedOutputFile := outputFile
		outputFile = android.PathForModuleOut(ctx, "unstripped", fileName)
		binary.stripper.strip(ctx, outputFile, strippedOutputFile, builderFlags)
	}

	if binary.Properties.Prefix_symbols != "" {
		afterPrefixSymbols := outputFile
		outputFile = android.PathForModuleOut(ctx, "unprefixed", fileName)
		TransformBinaryPrefixSymbols(ctx, binary.Properties.Prefix_symbols, outputFile,
			flagsToBuilderFlags(flags), afterPrefixSymbols)
	}

	TransformObjToDynamicBinary(ctx, objFiles, sharedLibs, deps.StaticLibs,
		deps.LateStaticLibs, deps.WholeStaticLibs, linkerDeps, deps.CrtBegin, deps.CrtEnd, true,
		builderFlags, outputFile)

	return ret
}

func (binary *binaryLinker) HostToolPath() android.OptionalPath {
	return binary.hostToolPath
}

type stripper struct {
	StripProperties StripProperties
}

func (stripper *stripper) needsStrip(ctx ModuleContext) bool {
	return !ctx.AConfig().EmbeddedInMake() && !stripper.StripProperties.Strip.None
}

func (stripper *stripper) strip(ctx ModuleContext, in, out android.ModuleOutPath,
	flags builderFlags) {
	if ctx.Darwin() {
		TransformDarwinStrip(ctx, in, out)
	} else {
		flags.stripKeepSymbols = stripper.StripProperties.Strip.Keep_symbols
		// TODO(ccross): don't add gnu debuglink for user builds
		flags.stripAddGnuDebuglink = true
		TransformStrip(ctx, in, out, flags)
	}
}

func testPerSrcMutator(mctx android.BottomUpMutatorContext) {
	if m, ok := mctx.Module().(*Module); ok {
		if test, ok := m.linker.(*testLinker); ok {
			if Bool(test.Properties.Test_per_src) {
				testNames := make([]string, len(m.compiler.(*baseCompiler).Properties.Srcs))
				for i, src := range m.compiler.(*baseCompiler).Properties.Srcs {
					testNames[i] = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
				}
				tests := mctx.CreateLocalVariations(testNames...)
				for i, src := range m.compiler.(*baseCompiler).Properties.Srcs {
					tests[i].(*Module).compiler.(*baseCompiler).Properties.Srcs = []string{src}
					tests[i].(*Module).linker.(*testLinker).binaryLinker.Properties.Stem = testNames[i]
				}
			}
		}
	}
}

type testLinker struct {
	binaryLinker
	Properties TestLinkerProperties
}

func (test *testLinker) begin(ctx BaseModuleContext) {
	test.binaryLinker.begin(ctx)

	runpath := "../../lib"
	if ctx.toolchain().Is64Bit() {
		runpath += "64"
	}
	test.dynamicProperties.RunPaths = append([]string{runpath}, test.dynamicProperties.RunPaths...)
}

func (test *testLinker) props() []interface{} {
	return append(test.binaryLinker.props(), &test.Properties)
}

func (test *testLinker) flags(ctx ModuleContext, flags Flags) Flags {
	flags = test.binaryLinker.flags(ctx, flags)

	if !test.Properties.Gtest {
		return flags
	}

	flags.CFlags = append(flags.CFlags, "-DGTEST_HAS_STD_STRING")
	if ctx.Host() {
		flags.CFlags = append(flags.CFlags, "-O0", "-g")

		switch ctx.Os() {
		case android.Windows:
			flags.CFlags = append(flags.CFlags, "-DGTEST_OS_WINDOWS")
		case android.Linux:
			flags.CFlags = append(flags.CFlags, "-DGTEST_OS_LINUX")
			flags.LdFlags = append(flags.LdFlags, "-lpthread")
		case android.Darwin:
			flags.CFlags = append(flags.CFlags, "-DGTEST_OS_MAC")
			flags.LdFlags = append(flags.LdFlags, "-lpthread")
		}
	} else {
		flags.CFlags = append(flags.CFlags, "-DGTEST_OS_LINUX_ANDROID")
	}

	return flags
}

func (test *testLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	if test.Properties.Gtest {
		if ctx.sdk() && ctx.Device() {
			switch ctx.selectedStl() {
			case "ndk_libc++_shared", "ndk_libc++_static":
				deps.StaticLibs = append(deps.StaticLibs, "libgtest_main_ndk_libcxx", "libgtest_ndk_libcxx")
			case "ndk_libgnustl_static":
				deps.StaticLibs = append(deps.StaticLibs, "libgtest_main_ndk_gnustl", "libgtest_ndk_gnustl")
			default:
				deps.StaticLibs = append(deps.StaticLibs, "libgtest_main_ndk", "libgtest_ndk")
			}
		} else {
			deps.StaticLibs = append(deps.StaticLibs, "libgtest_main", "libgtest")
		}
	}
	deps = test.binaryLinker.deps(ctx, deps)
	return deps
}

type testInstaller struct {
	baseInstaller
}

func (installer *testInstaller) install(ctx ModuleContext, file android.Path) {
	installer.dir = filepath.Join(installer.dir, ctx.ModuleName())
	installer.dir64 = filepath.Join(installer.dir64, ctx.ModuleName())
	installer.baseInstaller.install(ctx, file)
}

func NewTest(hod android.HostOrDeviceSupported) *Module {
	module := newModule(hod, android.MultilibBoth)
	module.compiler = &baseCompiler{}
	linker := &testLinker{}
	linker.Properties.Gtest = true
	module.linker = linker
	module.installer = &testInstaller{
		baseInstaller: baseInstaller{
			dir:   "nativetest",
			dir64: "nativetest64",
			data:  true,
		},
	}
	return module
}

func testFactory() (blueprint.Module, []interface{}) {
	module := NewTest(android.HostAndDeviceSupported)
	return module.Init()
}

type benchmarkLinker struct {
	binaryLinker
}

func (benchmark *benchmarkLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	deps = benchmark.binaryLinker.deps(ctx, deps)
	deps.StaticLibs = append(deps.StaticLibs, "libbenchmark", "libbase")
	return deps
}

func NewBenchmark(hod android.HostOrDeviceSupported) *Module {
	module := newModule(hod, android.MultilibFirst)
	module.compiler = &baseCompiler{}
	module.linker = &benchmarkLinker{}
	module.installer = &baseInstaller{
		dir:   "nativetest",
		dir64: "nativetest64",
		data:  true,
	}
	return module
}

func benchmarkFactory() (blueprint.Module, []interface{}) {
	module := NewBenchmark(android.HostAndDeviceSupported)
	return module.Init()
}

//
// Static library
//

func libraryStaticFactory() (blueprint.Module, []interface{}) {
	module := NewLibrary(android.HostAndDeviceSupported, false, true)
	return module.Init()
}

//
// Shared libraries
//

func librarySharedFactory() (blueprint.Module, []interface{}) {
	module := NewLibrary(android.HostAndDeviceSupported, true, false)
	return module.Init()
}

//
// Host static library
//

func libraryHostStaticFactory() (blueprint.Module, []interface{}) {
	module := NewLibrary(android.HostSupported, false, true)
	return module.Init()
}

//
// Host Shared libraries
//

func libraryHostSharedFactory() (blueprint.Module, []interface{}) {
	module := NewLibrary(android.HostSupported, true, false)
	return module.Init()
}

//
// Host Binaries
//

func binaryHostFactory() (blueprint.Module, []interface{}) {
	module := NewBinary(android.HostSupported)
	return module.Init()
}

//
// Host Tests
//

func testHostFactory() (blueprint.Module, []interface{}) {
	module := NewTest(android.HostSupported)
	return module.Init()
}

//
// Host Benchmarks
//

func benchmarkHostFactory() (blueprint.Module, []interface{}) {
	module := NewBenchmark(android.HostSupported)
	return module.Init()
}

//
// Defaults
//
type Defaults struct {
	android.ModuleBase
	android.DefaultsModule
}

func (*Defaults) GenerateAndroidBuildActions(ctx android.ModuleContext) {
}

func defaultsFactory() (blueprint.Module, []interface{}) {
	module := &Defaults{}

	propertyStructs := []interface{}{
		&BaseProperties{},
		&BaseCompilerProperties{},
		&BaseLinkerProperties{},
		&LibraryCompilerProperties{},
		&FlagExporterProperties{},
		&LibraryLinkerProperties{},
		&BinaryLinkerProperties{},
		&TestLinkerProperties{},
		&UnusedProperties{},
		&StlProperties{},
		&SanitizeProperties{},
		&StripProperties{},
	}

	_, propertyStructs = android.InitAndroidArchModule(module, android.HostAndDeviceDefault,
		android.MultilibDefault, propertyStructs...)

	return android.InitDefaultsModule(module, module, propertyStructs...)
}

//
// Device libraries shipped with gcc
//

type toolchainLibraryLinker struct {
	baseLinker
}

var _ baseLinkerInterface = (*toolchainLibraryLinker)(nil)

func (*toolchainLibraryLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	// toolchain libraries can't have any dependencies
	return deps
}

func (*toolchainLibraryLinker) buildStatic() bool {
	return true
}

func (*toolchainLibraryLinker) buildShared() bool {
	return false
}

func toolchainLibraryFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	module.compiler = &baseCompiler{}
	module.linker = &toolchainLibraryLinker{}
	module.Properties.Clang = proptools.BoolPtr(false)
	return module.Init()
}

func (library *toolchainLibraryLinker) link(ctx ModuleContext,
	flags Flags, deps PathDeps, objFiles android.Paths) android.Path {

	libName := ctx.ModuleName() + staticLibraryExtension
	outputFile := android.PathForModuleOut(ctx, libName)

	if flags.Clang {
		ctx.ModuleErrorf("toolchain_library must use GCC, not Clang")
	}

	CopyGccLib(ctx, libName, flagsToBuilderFlags(flags), outputFile)

	ctx.CheckbuildFile(outputFile)

	return outputFile
}

func (*toolchainLibraryLinker) installable() bool {
	return false
}

// NDK prebuilt libraries.
//
// These differ from regular prebuilts in that they aren't stripped and usually aren't installed
// either (with the exception of the shared STLs, which are installed to the app's directory rather
// than to the system image).

func getNdkLibDir(ctx android.ModuleContext, toolchain Toolchain, version string) android.SourcePath {
	suffix := ""
	// Most 64-bit NDK prebuilts store libraries in "lib64", except for arm64 which is not a
	// multilib toolchain and stores the libraries in "lib".
	if toolchain.Is64Bit() && ctx.Arch().ArchType != android.Arm64 {
		suffix = "64"
	}
	return android.PathForSource(ctx, fmt.Sprintf("prebuilts/ndk/current/platforms/android-%s/arch-%s/usr/lib%s",
		version, toolchain.Name(), suffix))
}

func ndkPrebuiltModuleToPath(ctx android.ModuleContext, toolchain Toolchain,
	ext string, version string) android.Path {

	// NDK prebuilts are named like: ndk_NAME.EXT.SDK_VERSION.
	// We want to translate to just NAME.EXT
	name := strings.Split(strings.TrimPrefix(ctx.ModuleName(), "ndk_"), ".")[0]
	dir := getNdkLibDir(ctx, toolchain, version)
	return dir.Join(ctx, name+ext)
}

type ndkPrebuiltObjectLinker struct {
	objectLinker
}

func (*ndkPrebuiltObjectLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	// NDK objects can't have any dependencies
	return deps
}

func ndkPrebuiltObjectFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	module.linker = &ndkPrebuiltObjectLinker{}
	return module.Init()
}

func (c *ndkPrebuiltObjectLinker) link(ctx ModuleContext, flags Flags,
	deps PathDeps, objFiles android.Paths) android.Path {
	// A null build step, but it sets up the output path.
	if !strings.HasPrefix(ctx.ModuleName(), "ndk_crt") {
		ctx.ModuleErrorf("NDK prebuilts must have an ndk_crt prefixed name")
	}

	return ndkPrebuiltModuleToPath(ctx, flags.Toolchain, objectExtension, ctx.sdkVersion())
}

type ndkPrebuiltLibraryLinker struct {
	libraryLinker
}

var _ baseLinkerInterface = (*ndkPrebuiltLibraryLinker)(nil)
var _ exportedFlagsProducer = (*libraryLinker)(nil)

func (ndk *ndkPrebuiltLibraryLinker) props() []interface{} {
	return append(ndk.libraryLinker.props(), &ndk.Properties, &ndk.flagExporter.Properties)
}

func (*ndkPrebuiltLibraryLinker) deps(ctx BaseModuleContext, deps Deps) Deps {
	// NDK libraries can't have any dependencies
	return deps
}

func ndkPrebuiltLibraryFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	linker := &ndkPrebuiltLibraryLinker{}
	linker.dynamicProperties.BuildShared = true
	module.linker = linker
	return module.Init()
}

func (ndk *ndkPrebuiltLibraryLinker) link(ctx ModuleContext, flags Flags,
	deps PathDeps, objFiles android.Paths) android.Path {
	// A null build step, but it sets up the output path.
	if !strings.HasPrefix(ctx.ModuleName(), "ndk_lib") {
		ctx.ModuleErrorf("NDK prebuilts must have an ndk_lib prefixed name")
	}

	ndk.exportIncludes(ctx, "-isystem")

	return ndkPrebuiltModuleToPath(ctx, flags.Toolchain, flags.Toolchain.ShlibSuffix(),
		ctx.sdkVersion())
}

// The NDK STLs are slightly different from the prebuilt system libraries:
//     * Are not specific to each platform version.
//     * The libraries are not in a predictable location for each STL.

type ndkPrebuiltStlLinker struct {
	ndkPrebuiltLibraryLinker
}

func ndkPrebuiltSharedStlFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	linker := &ndkPrebuiltStlLinker{}
	linker.dynamicProperties.BuildShared = true
	module.linker = linker
	return module.Init()
}

func ndkPrebuiltStaticStlFactory() (blueprint.Module, []interface{}) {
	module := newBaseModule(android.DeviceSupported, android.MultilibBoth)
	linker := &ndkPrebuiltStlLinker{}
	linker.dynamicProperties.BuildStatic = true
	module.linker = linker
	return module.Init()
}

func getNdkStlLibDir(ctx android.ModuleContext, toolchain Toolchain, stl string) android.SourcePath {
	gccVersion := toolchain.GccVersion()
	var libDir string
	switch stl {
	case "libstlport":
		libDir = "cxx-stl/stlport/libs"
	case "libc++":
		libDir = "cxx-stl/llvm-libc++/libs"
	case "libgnustl":
		libDir = fmt.Sprintf("cxx-stl/gnu-libstdc++/%s/libs", gccVersion)
	}

	if libDir != "" {
		ndkSrcRoot := "prebuilts/ndk/current/sources"
		return android.PathForSource(ctx, ndkSrcRoot).Join(ctx, libDir, ctx.Arch().Abi[0])
	}

	ctx.ModuleErrorf("Unknown NDK STL: %s", stl)
	return android.PathForSource(ctx, "")
}

func (ndk *ndkPrebuiltStlLinker) link(ctx ModuleContext, flags Flags,
	deps PathDeps, objFiles android.Paths) android.Path {
	// A null build step, but it sets up the output path.
	if !strings.HasPrefix(ctx.ModuleName(), "ndk_lib") {
		ctx.ModuleErrorf("NDK prebuilts must have an ndk_lib prefixed name")
	}

	ndk.exportIncludes(ctx, "-I")

	libName := strings.TrimPrefix(ctx.ModuleName(), "ndk_")
	libExt := flags.Toolchain.ShlibSuffix()
	if ndk.dynamicProperties.BuildStatic {
		libExt = staticLibraryExtension
	}

	stlName := strings.TrimSuffix(libName, "_shared")
	stlName = strings.TrimSuffix(stlName, "_static")
	libDir := getNdkStlLibDir(ctx, flags.Toolchain, stlName)
	return libDir.Join(ctx, libName+libExt)
}

func linkageMutator(mctx android.BottomUpMutatorContext) {
	if m, ok := mctx.Module().(*Module); ok {
		if m.linker != nil {
			if linker, ok := m.linker.(baseLinkerInterface); ok {
				var modules []blueprint.Module
				if linker.buildStatic() && linker.buildShared() {
					modules = mctx.CreateLocalVariations("static", "shared")
					static := modules[0].(*Module)
					shared := modules[1].(*Module)

					static.linker.(baseLinkerInterface).setStatic(true)
					shared.linker.(baseLinkerInterface).setStatic(false)

					if staticCompiler, ok := static.compiler.(*libraryCompiler); ok {
						sharedCompiler := shared.compiler.(*libraryCompiler)
						if len(staticCompiler.Properties.Static.Cflags) == 0 &&
							len(sharedCompiler.Properties.Shared.Cflags) == 0 {
							// Optimize out compiling common .o files twice for static+shared libraries
							mctx.AddInterVariantDependency(reuseObjTag, shared, static)
							sharedCompiler.baseCompiler.Properties.Srcs = nil
						}
					}
				} else if linker.buildStatic() {
					modules = mctx.CreateLocalVariations("static")
					modules[0].(*Module).linker.(baseLinkerInterface).setStatic(true)
				} else if linker.buildShared() {
					modules = mctx.CreateLocalVariations("shared")
					modules[0].(*Module).linker.(baseLinkerInterface).setStatic(false)
				} else {
					panic(fmt.Errorf("library %q not static or shared", mctx.ModuleName()))
				}
			}
		}
	}
}

// lastUniqueElements returns all unique elements of a slice, keeping the last copy of each
// modifies the slice contents in place, and returns a subslice of the original slice
func lastUniqueElements(list []string) []string {
	totalSkip := 0
	for i := len(list) - 1; i >= totalSkip; i-- {
		skip := 0
		for j := i - 1; j >= totalSkip; j-- {
			if list[i] == list[j] {
				skip++
			} else {
				list[j+skip] = list[j]
			}
		}
		totalSkip += skip
	}
	return list[totalSkip:]
}

var Bool = proptools.Bool
