# Included by the Makefile (CMAKE_PROJECT_INCLUDE). With a shared Qt, Qt's QML
# import scan links nothing and only warns "qml plugin ... will not be linked".
# This deferred call runs before Qt's finalizer and turns the scan off.
if(NOT COMMAND _wisp_skip_qml_import_scan)
  function(_wisp_skip_qml_import_scan)
    if(TARGET Qt6::Core)
      get_target_property(_qt_core_type Qt6::Core TYPE)
      foreach(_t wisp wisp-uitest)
        if(TARGET ${_t} AND _qt_core_type STREQUAL "SHARED_LIBRARY")
          set_target_properties(${_t} PROPERTIES QT_QML_MODULE_NO_IMPORT_SCAN TRUE)
        endif()
      endforeach()
    endif()
  endfunction()
  cmake_language(DEFER CALL _wisp_skip_qml_import_scan)
endif()
