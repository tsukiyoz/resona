# Apple Silicon always has NEON; Opus 1.6.1 has no Darwin ARM runtime detector.
# Run after project() so these are target properties, not the build host's uname.
if(NOT OPUS_DISABLE_INTRINSICS AND CMAKE_SYSTEM_NAME STREQUAL "Darwin" AND
   ((NOT CMAKE_OSX_ARCHITECTURES AND CMAKE_SYSTEM_PROCESSOR MATCHES "^(arm64|aarch64)$") OR
    CMAKE_OSX_ARCHITECTURES STREQUAL "arm64"))
  set(OPUS_MAY_HAVE_NEON OFF CACHE BOOL "Does runtime check for neon support" FORCE)
  set(OPUS_PRESUME_NEON ON CACHE BOOL "Assume target CPU has NEON support" FORCE)
  # Opus headers still need these declarations when runtime dispatch is disabled.
  add_compile_definitions(OPUS_ARM_MAY_HAVE_NEON OPUS_ARM_MAY_HAVE_NEON_INTR)
endif()
