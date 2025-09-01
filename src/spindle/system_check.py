"""System dependency checking and platform detection."""

import logging
import platform
import shutil
from dataclasses import dataclass
from typing import ClassVar

logger = logging.getLogger(__name__)


@dataclass
class SystemDependency:
    """Represents a system dependency with install instructions."""

    name: str
    binary: str
    required: bool
    description: str
    debian_package: str | None = None
    rhel_package: str | None = None
    arch_package: str | None = None
    install_url: str | None = None


@dataclass
class DependencyStatus:
    """Status of system dependencies check."""

    available: dict[str, bool]
    missing_required: list[str]
    missing_optional: list[str]
    platform_info: dict[str, str]


class SystemDependencyChecker:
    """Check system dependencies and provide installation guidance."""

    DEPENDENCIES: ClassVar[list[SystemDependency]] = [
        SystemDependency(
            name="MakeMKV",
            binary="makemkvcon",
            required=True,
            description="DVD/Blu-ray ripping tool",
            install_url="https://www.makemkv.com/download/",
            debian_package=None,  # Usually installed manually
            rhel_package=None,
            arch_package="makemkv",
        ),
        SystemDependency(
            name="drapto",
            binary="drapto",
            required=True,
            description="AV1 video encoder",
            install_url="https://github.com/alexheretic/drapto",
            debian_package=None,  # Install via cargo
            rhel_package=None,
            arch_package=None,
        ),
        SystemDependency(
            name="eject utility",
            binary="eject",
            required=False,
            description="Disc ejection tool",
            debian_package="eject",
            rhel_package="util-linux",
            arch_package="util-linux",
        ),
    ]

    def __init__(self) -> None:
        self.platform_info = self._detect_platform()

    def check_all_dependencies(self) -> DependencyStatus:
        """Check all system dependencies."""
        available = {}
        missing_required = []
        missing_optional = []

        for dep in self.DEPENDENCIES:
            is_available = self._check_binary_available(dep.binary)

            available[dep.name] = is_available

            if not is_available:
                if dep.required:
                    missing_required.append(dep.name)
                else:
                    missing_optional.append(dep.name)

        return DependencyStatus(
            available=available,
            missing_required=missing_required,
            missing_optional=missing_optional,
            platform_info=self.platform_info,
        )

    def log_dependency_status(
        self,
        status: DependencyStatus,
        show_available: bool = False,
    ) -> None:
        """Log dependency status with helpful messages."""
        if show_available:
            available_deps = [name for name, avail in status.available.items() if avail]
            if available_deps:
                logger.info(f"Available dependencies: {', '.join(available_deps)}")

        # Log missing required dependencies
        if status.missing_required:
            logger.error("MISSING REQUIRED DEPENDENCIES:")
            for dep_name in status.missing_required:
                dep = self._get_dependency(dep_name)
                if dep:
                    logger.error(f"  • {dep.name}: {dep.description}")
                    self._log_install_instructions(dep)

        # Log missing optional dependencies
        if status.missing_optional:
            logger.warning("Missing optional dependencies (features will be disabled):")
            for dep_name in status.missing_optional:
                dep = self._get_dependency(dep_name)
                if dep:
                    logger.warning(f"  • {dep.name}: {dep.description}")
                    self._log_install_instructions(dep, level="info")

    def validate_required_dependencies(self, log_status: bool = True) -> bool:
        """Check required dependencies and return False if any are missing."""
        status = self.check_all_dependencies()

        if status.missing_required:
            if log_status:
                self.log_dependency_status(status)
                logger.error("Cannot start Spindle with missing required dependencies")
            return False

        # Log optional dependency warnings
        if status.missing_optional and log_status:
            self.log_dependency_status(status)

        return True

    def _check_binary_available(self, binary_name: str) -> bool:
        """Check if a binary is available in PATH."""
        return shutil.which(binary_name) is not None

    def _check_permissions(self, dep: SystemDependency) -> bool:
        """Check if user has required permissions for the dependency."""
        # For all dependencies, just return True since the binary exists
        return True

    def _get_dependency(self, name: str) -> SystemDependency | None:
        """Get dependency info by name."""
        for dep in self.DEPENDENCIES:
            if dep.name == name:
                return dep
        return None

    def _detect_platform(self) -> dict[str, str]:
        """Detect platform information."""
        info = {
            "system": platform.system(),
            "machine": platform.machine(),
            "python_version": platform.python_version(),
        }

        # Detect Linux distribution
        if info["system"] == "Linux":
            try:
                # Try to read /etc/os-release
                with open("/etc/os-release") as f:
                    for line in f:
                        if line.startswith("ID="):
                            info["distro"] = line.split("=")[1].strip().strip('"')
                            break
                        if line.startswith("ID_LIKE="):
                            info["distro_like"] = line.split("=")[1].strip().strip('"')
            except FileNotFoundError:
                info["distro"] = "unknown"

        return info

    def _log_install_instructions(
        self,
        dep: SystemDependency,
        level: str = "warning",
    ) -> None:
        """Log platform-specific installation instructions."""
        log_func = getattr(logger, level)

        if dep.install_url:
            log_func(f"    Install from: {dep.install_url}")

        # Platform-specific package installation
        distro = self.platform_info.get("distro", "").lower()
        distro_like = self.platform_info.get("distro_like", "").lower()

        if "debian" in distro or "ubuntu" in distro or "debian" in distro_like:
            if dep.debian_package:
                log_func(f"    Debian/Ubuntu: sudo apt install {dep.debian_package}")

        elif (
            "rhel" in distro
            or "centos" in distro
            or "rocky" in distro
            or "fedora" in distro
        ):
            if dep.rhel_package:
                if "fedora" in distro:
                    log_func(f"    Fedora: sudo dnf install {dep.rhel_package}")
                else:
                    log_func(f"    RHEL/CentOS: sudo dnf install {dep.rhel_package}")

        elif "arch" in distro or "manjaro" in distro:
            if dep.arch_package:
                log_func(f"    Arch Linux: sudo pacman -S {dep.arch_package}")

        # Special cases
        if dep.name == "drapto":
            log_func("    Install with Rust: cargo install drapto")
        elif dep.name == "MakeMKV":
            log_func("    Note: MakeMKV requires manual installation and license key")


def check_system_dependencies(
    validate_required: bool = False,
    log_status: bool = True,
) -> DependencyStatus:
    """Check system dependencies.

    Args:
        validate_required: If True, exit if required dependencies are missing
        log_status: If True, log dependency status (default: True)

    Returns:
        DependencyStatus with availability information
    """
    checker = SystemDependencyChecker()

    if validate_required:
        if not checker.validate_required_dependencies(log_status=log_status):
            import sys

            sys.exit(1)
        # Return status after successful validation
        return checker.check_all_dependencies()
    # Only log status if requested
    status = checker.check_all_dependencies()
    if log_status:
        checker.log_dependency_status(status, show_available=True)
    return status
