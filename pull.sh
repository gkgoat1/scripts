if [ -d "$2" ] ; then
cd $2; git pull https://github.com/$1/$2
else
git clone https://github.com/$1/$2
fi
