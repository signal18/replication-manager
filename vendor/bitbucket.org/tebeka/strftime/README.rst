`strftime`_ For Go
==================

Q: Why, we already have `time.Format`_?

A: Yes, but it becomes tricky to use if if you have string with things other
than time in them. (like `/path/to/%Y/%m/%d/report`)


.. _strftime:  http://docs.python.org/2/library/time.html#time.strftime
.. _`time.Format`: http://golang.org/pkg/time/#Time.Format


Installing
==========
::

    go get bitbucket.org/tebeka/strftime

Example
=======
::

    str, err := strftime.Format("%Y/%m/%d", time.Now())


Contact
=======
https://bitbucket.org/tebeka/strftime
    
License
=======
MIT (see `LICENSE.txt`_)

.. _`LICENSE.txt`: https://bitbucket.org/tebeka/strftime/src/tip/LICENSE.txt
