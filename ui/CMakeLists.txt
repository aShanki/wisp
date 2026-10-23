cmake_minimum_required(VERSION 3.21)
project(wisp VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE RelWithDebInfo)
endif()

find_package(Qt6 6.9 REQUIRED COMPONENTS Core Gui Qml Quick QuickControls2 Network)
qt_standard_project_setup(REQUIRES 6.9)

# Qt translation units take 300-500 MB each; keep parallelism low.
set_property(GLOBAL PROPERTY JOB_POOLS wisp_compile=1 wisp_link=1)
set(CMAKE_JOB_POOL_COMPILE wisp_compile)
set(CMAKE_JOB_POOL_LINK wisp_link)
qt_policy(SET QTP0004 NEW)

# Sources and QML are globbed; re-run cmake after adding files.
file(GLOB WISP_CPP CONFIGURE_DEPENDS
  ${CMAKE_CURRENT_SOURCE_DIR}/src/*.cpp ${CMAKE_CURRENT_SOURCE_DIR}/src/*.h
  ${CMAKE_CURRENT_SOURCE_DIR}/src/3rdparty/*.cpp ${CMAKE_CURRENT_SOURCE_DIR}/src/3rdparty/*.hpp)
list(FILTER WISP_CPP EXCLUDE REGEX "/src/main\\.cpp$")

file(GLOB_RECURSE WISP_QML_ABS CONFIGURE_DEPENDS ${CMAKE_CURRENT_SOURCE_DIR}/qml/*.qml ${CMAKE_CURRENT_SOURCE_DIR}/qml/*.js)
set(WISP_QML "")
foreach(f ${WISP_QML_ABS})
  file(RELATIVE_PATH rel ${CMAKE_CURRENT_SOURCE_DIR} ${f})
  list(APPEND WISP_QML ${rel})
  get_filename_component(base ${f} NAME_WE)
  if(base STREQUAL "Theme")
    set_source_files_properties(${rel} PROPERTIES QT_QML_SINGLETON_TYPE TRUE)
  endif()
endforeach()

file(GLOB_RECURSE WISP_ASSETS CONFIGURE_DEPENDS RELATIVE ${CMAKE_CURRENT_SOURCE_DIR}
  ${CMAKE_CURRENT_SOURCE_DIR}/assets/*.svg ${CMAKE_CURRENT_SOURCE_DIR}/assets/*.png
  ${CMAKE_CURRENT_SOURCE_DIR}/assets/*.tsv)

# The module is a static library so the app and the interaction test share it.
qt_add_library(wispui STATIC)
qt_add_qml_module(wispui
  URI Wisp
  VERSION 1.0
  QML_FILES ${WISP_QML}
  SOURCES ${WISP_CPP}
)
qt_add_resources(wispui wisp_assets PREFIX / FILES ${WISP_ASSETS})

target_include_directories(wispui PUBLIC src)
target_link_libraries(wispui PUBLIC
  Qt6::Core Qt6::Gui Qt6::Qml Qt6::Quick Qt6::QuickControls2 Qt6::Network)
target_compile_definitions(wispui PUBLIC WISP_VERSION="${PROJECT_VERSION}")

qt_add_executable(wisp src/main.cpp)
target_link_libraries(wisp PRIVATE wispui wispuiplugin)
if(APPLE)
  enable_language(OBJCXX)
  target_sources(wisp PRIVATE src/macos.mm)
  set_source_files_properties(src/macos.mm PROPERTIES COMPILE_OPTIONS "-fobjc-arc")
  target_link_libraries(wisp PRIVATE "-framework AppKit" "-framework UserNotifications")
  set_target_properties(wisp PROPERTIES MACOSX_BUNDLE TRUE
    MACOSX_BUNDLE_BUNDLE_NAME Wisp
    MACOSX_BUNDLE_GUI_IDENTIFIER io.github.aShanki.wisp
    MACOSX_BUNDLE_INFO_PLIST "${CMAKE_CURRENT_SOURCE_DIR}/../packaging/Info.plist")
endif()
install(TARGETS wisp RUNTIME DESTINATION bin BUNDLE DESTINATION .)

# Tests against wispd --mock: the interaction test (wisp-uitest) and the
# seeded chaos monkey (wisp-chaos, which also needs Qt Quick Test).
find_package(Qt6 QUIET COMPONENTS Test QuickTest)
if(TARGET Qt6::Test AND TARGET Qt6::QuickTest)
  set(WISP_TESTS uitest chaos)
  foreach(test ${WISP_TESTS})
    qt_add_executable(wisp-${test} EXCLUDE_FROM_ALL tests/${test}.cpp tests/harness.h)
    target_link_libraries(wisp-${test} PRIVATE wispui wispuiplugin Qt6::Test Qt6::QuickTest)
    target_compile_definitions(wisp-${test} PRIVATE WISP_SOURCE_DIR="${CMAKE_CURRENT_SOURCE_DIR}")
  endforeach()
endif()
